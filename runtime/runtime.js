// Compatibility entry point.
//
// The canonical Vela runtime implementation is ../runtime.js. New tooling and
// generated components use that asset; this module preserves existing imports
// that still resolve through runtime/runtime.js without duplicating behaviour.
export * from '../runtime.js';
