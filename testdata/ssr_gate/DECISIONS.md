
## 2026-10-09: initial expectations

Recorded from the first gate run (task 031).

## 2026-10-18: task 057: router.getCurrent() is now a method call on an object (T-7.1c), so the diagnostic reaches the real cause, the side effects of src/simple-router.js, and names the host for createRouter; App.vela failed before and still fails

Accepted regressions:

- qdash App.vela: status error (VELA-SSR-EXPR) → error (VELA-SSR-HOST-MISSING)

## 2026-11-10: task 101: event modifiers are supported, so @keydown.enter in deepseek-todo App.vela is valid

The negative project still fails with VELA-DERIVED-CALLBACK, VELA-CLASS-OBJECT and VELA-BIND-MEMBER; VELA-EVENT-MODIFIER is no longer required.
