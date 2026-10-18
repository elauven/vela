
## 2026-10-09: initial expectations

Recorded from the first gate run (task 031).

## 2026-10-18: task 057: router.getCurrent() is now a method call on an object (T-7.1c), so the diagnostic reaches the real cause, the side effects of src/simple-router.js, and names the host for createRouter; App.vela failed before and still fails

Accepted regressions:

- qdash App.vela: status error (VELA-SSR-EXPR) → error (VELA-SSR-HOST-MISSING)
