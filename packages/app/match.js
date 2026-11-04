// Route matching of Vela App (docs/APP.md P-6, P-8, P-13). The Go package
// github.com/elauven/vela/app implements the same rules; the shared
// fixtures in app/testdata test both.

// The built-in matchers (P-13).
export function int(segment) {
  return /^[0-9]+$/.test(segment);
}

export function uuid(segment) {
  return /^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$/.test(segment);
}

export function slug(segment) {
  return /^[a-z0-9]+(-[a-z0-9]+)*$/.test(segment);
}

export const builtinMatchers = Object.freeze({ int, uuid, slug });

// splitPath splits an absolute path into decoded segments before
// decoding each one, so %2F stays inside its segment (P-6). It returns
// null for a path that is not absolute or cannot be decoded.
function splitPath(path) {
  if (typeof path !== 'string' || path[0] !== '/') return null;
  if (path === '/') return [];
  const parts = path.slice(1).split('/');
  try {
    return parts.map((part) => decodeURIComponent(part));
  } catch {
    return null;
  }
}

function matchPattern(segments, path, matchers) {
  const params = {};
  for (let i = 0; i < segments.length; i++) {
    const segment = segments[i];
    if (segment.kind === 'rest') {
      params[segment.value] = path.slice(i).join('/');
      return params;
    }
    if (i >= path.length) return null;
    const value = path[i];
    if (segment.kind === 'static') {
      if (value !== segment.value) return null;
      continue;
    }
    if (value === '') return null;
    if (segment.matcher) {
      const match = matchers[segment.matcher];
      if (!match || !match(value)) return null;
    }
    params[segment.value] = value;
  }
  return segments.length === path.length ? params : null;
}

// matchPath finds the route of a path in a generated table: { routes,
// patterns, matchers }. Patterns are in ranking order, and the first full
// match wins. It returns { route, params } or null.
export function matchPath(table, path) {
  const segments = splitPath(path);
  if (!segments) return null;
  for (const pattern of table.patterns) {
    const params = matchPattern(pattern.segments, segments, table.matchers);
    if (params) return { route: table.routes[pattern.route], params };
  }
  return null;
}
