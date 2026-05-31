// Minimal ambient declarations for the Node built-in test modules, so the SDK
// can be type-checked and tested without an @types/node dependency. At runtime
// Node (>=18) provides the real implementations.

declare module 'node:test' {
  export function test(name: string, fn: () => void | Promise<void>): void;
}

declare module 'node:assert/strict' {
  interface AssertStrict {
    (value: unknown, message?: string): void;
    equal(actual: unknown, expected: unknown, message?: string): void;
    deepEqual(actual: unknown, expected: unknown, message?: string): void;
    ok(value: unknown, message?: string): void;
    rejects(fn: () => Promise<unknown>, check?: (err: unknown) => boolean): Promise<void>;
  }
  const assert: AssertStrict;
  export default assert;
}
