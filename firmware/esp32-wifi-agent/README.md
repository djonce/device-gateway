# ESP32 Wi-Fi Agent

This firmware connects an ESP32 development board directly to Light Gateway over Wi-Fi.

## Behavior

- Starts a setup AP when Wi-Fi is not configured.
- Registers itself with the gateway and stores the returned device token in NVS.
- Sends heartbeat every 10 seconds.
- Sends RSSI and heap telemetry every 30 seconds.
- Polls commands and supports:
  - `sensor.read`
  - `gpio.write`

## Setup Portal

After flashing, connect to the AP printed on serial:

```text
LightGateway-<chip-id>
```

Phones and laptops should automatically open the setup page after joining the AP. If the captive portal does not appear, open:

```text
http://192.168.4.1
```

The setup page scans nearby Wi-Fi networks and exposes SSIDs as selectable suggestions while still allowing hidden SSIDs to be typed manually.

Use the Mac LAN gateway URL:

```text
http://192.168.3.109:7001
```

Then enter Wi-Fi SSID/password and save.
