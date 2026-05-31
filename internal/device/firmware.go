package device

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// AddFirmware stores a firmware artifact (metadata + binary blob).
func (s *Store) AddFirmware(req AddFirmwareRequest, data []byte) (Firmware, error) {
	req.Version = strings.TrimSpace(req.Version)
	if req.Category == "" || req.Version == "" {
		return Firmware{}, fmt.Errorf("%w: category and version are required", ErrBadRequest)
	}
	if len(data) == 0 {
		return Firmware{}, fmt.Errorf("%w: firmware binary is empty", ErrBadRequest)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now().UTC()
	sum := sha256.Sum256(data)
	fw := Firmware{
		ID:        newID("fw", now),
		Category:  req.Category,
		Model:     strings.TrimSpace(req.Model),
		Version:   req.Version,
		Size:      int64(len(data)),
		SHA256:    hex.EncodeToString(sum[:]),
		Notes:     strings.TrimSpace(req.Notes),
		CreatedAt: now,
	}
	blob := append([]byte(nil), data...)
	s.firmwares[fw.ID] = fw
	s.firmwareBlobs[fw.ID] = blob
	s.appendEventLocked("firmware.uploaded", "", "firmware uploaded", map[string]any{
		"firmwareId": fw.ID, "category": fw.Category, "version": fw.Version,
	})
	if err := s.persistFirmwareLocked(fw, blob); err != nil {
		return Firmware{}, err
	}
	return fw, nil
}

// ListFirmware returns firmware metadata, newest first, optionally filtered by
// category (empty = all).
func (s *Store) ListFirmware(category DeviceCategory) []Firmware {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Firmware, 0, len(s.firmwares))
	for _, fw := range s.firmwares {
		if category != "" && fw.Category != category {
			continue
		}
		out = append(out, fw)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// GetFirmware returns one firmware's metadata.
func (s *Store) GetFirmware(id string) (Firmware, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	fw, ok := s.firmwares[id]
	if !ok {
		return Firmware{}, ErrNotFound
	}
	return fw, nil
}

// FirmwareBlob returns the binary for a firmware id.
func (s *Store) FirmwareBlob(id string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	blob, ok := s.firmwareBlobs[id]
	if !ok {
		return nil, ErrNotFound
	}
	return blob, nil
}

// SetDeviceTarget sets (or clears, with empty version) a device's desired
// firmware version. The device's next OTA poll will then pick it up.
func (s *Store) SetDeviceTarget(deviceID, version string) (Device, error) {
	version = strings.TrimSpace(version)
	s.mu.Lock()
	defer s.mu.Unlock()
	device, ok := s.devices[deviceID]
	if !ok {
		return Device{}, ErrNotFound
	}
	device.TargetFwVersion = version
	device.UpdatedAt = s.now().UTC()
	s.devices[deviceID] = device
	s.appendEventLocked("ota.target_set", deviceID, "ota target set", map[string]any{"version": version})
	if err := s.persistDeviceLocked(device); err != nil {
		return Device{}, err
	}
	return device, nil
}

// RolloutFirmware sets the target version for every device matching a firmware's
// category (and model, if the firmware specifies one). Returns affected count.
func (s *Store) RolloutFirmware(firmwareID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fw, ok := s.firmwares[firmwareID]
	if !ok {
		return 0, ErrNotFound
	}
	now := s.now().UTC()
	affected := 0
	for id, device := range s.devices {
		if device.Category != fw.Category {
			continue
		}
		if fw.Model != "" && device.Model != fw.Model {
			continue
		}
		device.TargetFwVersion = fw.Version
		device.UpdatedAt = now
		s.devices[id] = device
		if err := s.persistDeviceLocked(device); err != nil {
			return affected, err
		}
		affected++
	}
	s.appendEventLocked("ota.rollout", "", "firmware rollout", map[string]any{
		"firmwareId": fw.ID, "category": fw.Category, "version": fw.Version, "devices": affected,
	})
	return affected, nil
}

// ResolveOTA reports whether the device should update, and to which firmware.
// A device updates when it has a target version, a matching firmware exists for
// its category/model, and its current version differs from the target.
func (s *Store) ResolveOTA(deviceID string) (OTAStatus, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	device, ok := s.devices[deviceID]
	if !ok {
		return OTAStatus{}, ErrNotFound
	}
	status := OTAStatus{CurrentVersion: device.FwVersion, TargetVersion: device.TargetFwVersion}
	if device.TargetFwVersion == "" || device.TargetFwVersion == device.FwVersion {
		return status, nil
	}
	for _, fw := range s.firmwares {
		if fw.Category != device.Category || fw.Version != device.TargetFwVersion {
			continue
		}
		if fw.Model != "" && device.Model != "" && fw.Model != device.Model {
			continue
		}
		status.UpdateAvailable = true
		status.FirmwareID = fw.ID
		status.Version = fw.Version
		status.SHA256 = fw.SHA256
		status.Size = fw.Size
		status.DownloadURL = "/api/v1/devices/" + deviceID + "/firmware/" + fw.ID + "/download"
		break
	}
	return status, nil
}

func (s *Store) persistFirmwareLocked(fw Firmware, blob []byte) error {
	if s.storage != "sqlite" {
		return nil
	}
	raw, err := json.Marshal(fw)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`INSERT INTO firmware(id, data, blob) VALUES(?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET data=excluded.data, blob=excluded.blob`,
		fw.ID, string(raw), blob)
	return err
}

func (s *Store) loadSQLiteFirmware() error {
	rows, err := s.db.Query("SELECT data, blob FROM firmware")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		var blob []byte
		if err := rows.Scan(&raw, &blob); err != nil {
			return err
		}
		var fw Firmware
		if err := json.Unmarshal([]byte(raw), &fw); err != nil {
			return err
		}
		s.firmwares[fw.ID] = fw
		s.firmwareBlobs[fw.ID] = blob
	}
	return rows.Err()
}
