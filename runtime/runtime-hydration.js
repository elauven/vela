/**
 * Vela hydration runtime for the legacy SSR engine.
 *
 * Loaded only by SSR builds: components compiled for hydration import these
 * helpers from this module, and CSR-only builds do not ship it (R-12.2c).
 */

import { $$runtimeContract1 } from './runtime.js';
void $$runtimeContract1;

// $$createHydrationWalker indexes compiler-owned paired SSR markers without
// creating, moving, or deleting DOM nodes. Markers may nest, so validation is
// stack-based rather than a loose lookup by ID; crossed or duplicate ranges
// become actionable errors before a component attaches listeners or setters.
export function $$createHydrationWalker(root, componentId) {
  const label = componentId || 'unknown component';
  const doc = root && (root.ownerDocument || (root.nodeType === 9 ? root : null));
  if (!doc || typeof doc.createTreeWalker !== 'function') {
    throw new Error(`Vela hydration mismatch [${label}]: missing DOM root for marker walk`);
  }
  const walker = doc.createTreeWalker(root, 128); // NodeFilter.SHOW_COMMENT
  const markers = { v: new Map(), e: new Map() };
  const stack = [];
  const ignoredStructuralMarkers = [];
  let node;
  while ((node = walker.nextNode())) {
    const marker = String(node.data || '').trim();
    const match = /^\/?([ve]):([^:]+):(\d+)$/.exec(marker);
    if (!match) continue;
    const kind = match[1];
    if (match[2] !== componentId) continue;
    const id = Number(match[3]);
    const key = kind + ':' + id;
    const closing = marker[0] === '/';
    // Element marker IDs describe template positions, so an #each renders
    // the same inner e-id once per row. They are not global acquisition
    // targets while enclosed by a dynamic v-range; that range's own lowering
    // owns the active nodes. Keep validating its v boundaries while skipping
    // repeated structural element pairs.
    const repeatedStructuralMarker = !closing && stack.some(entry => entry.kind === 'v') && markers[kind].has(id);
    if (ignoredStructuralMarkers.length > 0 || repeatedStructuralMarker) {
      if (closing) {
        const open = ignoredStructuralMarkers.pop();
        if (!open || open.key !== key) {
          throw new Error(`Vela hydration mismatch [${label}]: structural marker end does not match its start`);
        }
      } else {
        ignoredStructuralMarkers.push({ key });
      }
      continue;
    }
    if (!closing) {
      if (markers[kind].has(id) || stack.some(entry => entry.key === key)) {
        throw new Error(`Vela hydration mismatch [${label}]: duplicate start marker <!--${kind}:${label}:${id}-->`);
      }
      stack.push({ kind, id, key, start: node });
      continue;
    }
    const open = stack.pop();
    if (!open) {
      throw new Error(`Vela hydration mismatch [${label}]: end marker <!--/${kind}:${label}:${id}--> has no start marker`);
    }
    if (open.key !== key) {
      throw new Error(`Vela hydration mismatch [${label}]: expected <!--/${open.kind}:${label}:${open.id}--> before <!--/${kind}:${label}:${id}-->`);
    }
    markers[kind].set(id, { start: open.start, end: node });
  }
  if (stack.length) {
    const open = stack[stack.length - 1];
    throw new Error(`Vela hydration mismatch [${label}]: start marker <!--${open.kind}:${label}:${open.id}--> has no end marker`);
  }
  if (ignoredStructuralMarkers.length !== 0) {
    throw new Error(`Vela hydration mismatch [${label}]: structural marker is not balanced`);
  }
  return { root, componentId: label, ranges: markers.v, elements: markers.e };
}

// $$acquireHydrationElement returns the existing element bracketed by one
// compiler-owned element range. It validates the tag rather than attempting a
// repair, so a stale server page cannot be silently adopted as another view.
export function $$acquireHydrationElement(walker, id, tag) {
  if (!walker || !(walker.elements instanceof Map)) {
    throw new Error('Vela hydration mismatch: invalid marker walker');
  }
  const range = walker.elements.get(id);
  if (!range) {
    throw new Error(`Vela hydration mismatch [${walker.componentId}]: missing element range e${id}`);
  }
  let node = range.start.nextSibling;
  while (node && node !== range.end && node.nodeType !== 1) node = node.nextSibling;
  if (!node || node === range.end) {
    throw new Error(`Vela hydration mismatch [${walker.componentId}]: element range e${id} contains no element`);
  }
  const actual = String(node.tagName || '').toLowerCase();
  const expected = String(tag || '').toLowerCase();
  // Browsers insert a tbody around bare table rows while parsing server HTML.
  // The compiler still owns the row marker range, whose opening marker stays
  // immediately before that implicit tbody. Treat only this standardized DOM
  // normalization as transparent; every other tag mismatch remains fatal.
  if (expected === 'tr' && actual === 'tbody') {
    const row = node.firstElementChild;
    if (row && String(row.tagName || '').toLowerCase() === 'tr') {
      return row;
    }
  }
  if (actual !== expected) {
    throw new Error(`Vela hydration mismatch [${walker.componentId}]: element range e${id} expected <${expected}> but found <${actual || 'unknown'}>`);
  }
  return node;
}

// $$acquireHydrationRange returns an already indexed SSR range. A generated
// hydrate entry point uses this for dynamic text and structural blocks; it
// never repairs a mismatch by clearing or recreating the component tree.
export function $$acquireHydrationRange(walker, id) {
  if (!walker || !(walker.ranges instanceof Map)) {
    throw new Error('Vela hydration mismatch: invalid marker walker');
  }
  const range = walker.ranges.get(id);
  if (!range) {
    throw new Error(`Vela hydration mismatch [${walker.componentId}]: missing marker range v${id}`);
  }
  return range;
}

// $$adoptHydrationRange snapshots the nodes already rendered between paired
// markers. It performs no writes, so an SSR branch remains untouched until a
// later reactive update explicitly replaces that structural range.
export function $$adoptHydrationRange(range) {
  if (!range || !range.start || !range.end) {
    throw new Error('Vela hydration mismatch: invalid structural range');
  }
  const nodes = [];
  for (let node = range.start.nextSibling; node && node !== range.end; node = node.nextSibling) {
    nodes.push(node);
  }
  if (!range.end.parentNode || (!nodes.length && range.start.nextSibling !== range.end)) {
    throw new Error('Vela hydration mismatch: malformed structural range');
  }
  return nodes;
}

// $$findHydrationNestedRange resolves one repeated structural v-range inside
// an already adopted keyed row. The component-wide walker intentionally does
// not index repeated markers below another v-range, so this helper makes the
// enclosing row the ownership boundary and rejects ambiguous/malformed pairs.
export function $$findHydrationNestedRange(range, componentId, id) {
  if (!range?.start || !range?.end) {
    throw new Error('Vela hydration mismatch: invalid nested structural owner range');
  }
  const marker = String(componentId || '') + ':' + String(id);
  const startMarker = 'v:' + marker;
  const endMarker = '/v:' + marker;
  let start = null;
  for (let node = range.start.nextSibling; node && node !== range.end; node = node.nextSibling) {
    if (node.nodeType !== 8) continue;
    const data = String(node.data || '');
    if (data === startMarker) {
      if (start) throw new Error('Vela hydration mismatch: duplicate nested structural start marker');
      start = node;
      continue;
    }
    if (data === endMarker) {
      if (!start) throw new Error('Vela hydration mismatch: unmatched nested structural end marker');
      return { start, end: node };
    }
  }
  if (start) throw new Error('Vela hydration mismatch: nested structural start marker has no end marker');
  throw new Error(`Vela hydration mismatch: missing nested marker range v${id}`);
}

// $$createHydrationRowController is the shared owner for one retained keyed
// row. Both CSR-created rows and later SSR-adopted rows use this exact shape,
// so cleanup ordering is independent of how the DOM range entered the tree.
export function $$createHydrationRowController(row, update, afterDestroy) {
  const controller = {
    row: row || null,
    updates: [],
    listeners: [],
    update: typeof update === 'function' ? update : () => {},
    destroy() {
      for (const cleanup of controller.listeners.slice().reverse()) cleanup();
      controller.listeners = [];
      if (typeof afterDestroy === 'function') afterDestroy();
      controller.updates = [];
    }
  };
  return controller;
}

// $$adoptHydrationKeyedRows maps existing direct row elements to the initial
// iterable. It is intentionally strict: a stale SSR response with a different
// row count or duplicate key is rejected before keyed reconciliation can move
// an unrelated DOM node under the wrong item.
export function $$adoptHydrationKeyedRows(range, iterable, keyOf, tag) {
  if (typeof keyOf !== 'function') {
    throw new Error('Vela hydration mismatch: keyed row adoption requires a key function');
  }
  const values = Array.from(iterable || []);
  const expected = String(tag || '').toLowerCase();
  const rows = [];
  for (let node = range?.start?.nextSibling; node && node !== range.end; node = node.nextSibling) {
    if (node.nodeType !== 1) continue;
	// Ordinary keyed rows supply their element tag. A direct component row has
	// no parent-visible tag in its AST, so an empty expected tag accepts every
	// direct element between the range markers; capability analysis guarantees
	// one component root per row before generated code reaches this helper.
    const tagName = String(node.tagName || '').toLowerCase();
    if (!expected && tagName === 'script' && String(node.id || '').startsWith('vela-state-')) continue;
    if (expected && tagName !== expected) continue;
    rows.push(node);
  }
  if (rows.length !== values.length) {
    throw new Error(`Vela hydration mismatch: keyed row count ${rows.length} does not match iterable length ${values.length}`);
  }
  const blocks = new Map();
  for (let index = 0; index < values.length; index++) {
    const key = keyOf(values[index], index);
    if (blocks.has(key)) {
      throw new Error(`Vela hydration mismatch: duplicate keyed row ${String(key)}`);
    }
    blocks.set(key, rows[index]);
  }
  return blocks;
}

// $$adoptHydrationKeyedRowRanges is the multi-root successor to element-row
// adoption. SSR emits r markers around every keyed iteration, so static
// siblings, several child roots, and payload scripts move as one row without
// a synthetic wrapper element.
export function $$adoptHydrationKeyedRowRanges(range, iterable, keyOf, markerID) {
  if (!range?.start || !range?.end || typeof keyOf !== 'function') throw new Error('Vela hydration mismatch: invalid keyed row range adoption');
  const values = Array.from(iterable || []), rows = [], suffix = ':' + String(markerID);
  let start = null;
  for (let node = range.start.nextSibling; node && node !== range.end; node = node.nextSibling) {
    if (node.nodeType !== 8) continue;
    const data = String(node.data || '');
    if (data.startsWith('r:') && data.endsWith(suffix)) { if (start) throw new Error('Vela hydration mismatch: nested keyed row marker'); start = node; continue; }
    if (data.startsWith('/r:') && data.endsWith(suffix)) { if (!start) throw new Error('Vela hydration mismatch: unmatched keyed row marker'); rows.push({start, end: node}); start = null; }
  }
  if (start) throw new Error('Vela hydration mismatch: unmatched keyed row marker');
  // Older SSR output predates row markers. Keep its single-element contract
  // hydratable while all newly rendered keyed rows take the strict range path.
  if (rows.length === 0) {
    for (let node = range.start.nextSibling; node && node !== range.end; node = node.nextSibling) {
      if (node.nodeType === 1 && !(String(node.tagName || '').toLowerCase() === 'script' && String(node.id || '').startsWith('vela-state-'))) rows.push({start: node, end: node});
    }
  }
  if (rows.length !== values.length) throw new Error(`Vela hydration mismatch: keyed row range count ${rows.length} does not match iterable length ${values.length}`);
  const blocks = new Map();
  for (let index = 0; index < values.length; index++) { const key = keyOf(values[index], index); if (blocks.has(key)) throw new Error(`Vela hydration mismatch: duplicate keyed row ${String(key)}`); blocks.set(key, rows[index]); }
  return blocks;
}

export function $$moveHydrationKeyedRowRange(end, row) {
  if (!end || !row?.start || !row?.end) throw new Error('Vela hydration mismatch: invalid keyed row range');
  const nodes = [];
  for (let node = row.start; node; node = node.nextSibling) { nodes.push(node); if (node === row.end) break; }
  if (nodes[nodes.length - 1] !== row.end) throw new Error('Vela hydration mismatch: detached keyed row range');
  for (const node of nodes) end.before(node);
}

export function $$removeHydrationKeyedRowRange(row) {
  if (!row?.start || !row?.end) throw new Error('Vela hydration mismatch: invalid keyed row range');
  for (let node = row.start; node;) {
    const next = node.nextSibling;
    node.remove();
    if (node === row.end) return;
    node = next;
  }
  throw new Error('Vela hydration mismatch: detached keyed row range');
}

// $$replaceHydrationKeyedRowContents replaces only the nodes enclosed by an
// existing keyed row boundary. The boundary itself remains stable so the
// reconciler can still reorder the retained key without remounting it.
export function $$replaceHydrationKeyedRowContents(row, fragment) {
  if (!row?.start || !row?.end || !row.end.parentNode) {
    throw new Error("Vela hydration mismatch: invalid keyed row replacement");
  }
  for (let node = row.start.nextSibling; node && node !== row.end;) {
    const next = node.nextSibling;
    node.remove();
    node = next;
  }
  if (row.start.nextSibling !== row.end) {
    throw new Error("Vela hydration mismatch: detached keyed row range");
  }
  row.end.before(fragment);
}

// $$reconcileHydrationKeyedRowRanges is the range-aware counterpart of the
// legacy element reconciler. `create` returns {start,end}; range boundaries
// travel with every child root and inert payload script as one keyed row.
export function $$reconcileHydrationKeyedRowRanges(range, blocks, iterable, keyOf, create, update) {
  if (!range?.end || !(blocks instanceof Map) || typeof keyOf !== 'function' || typeof create !== 'function') throw new Error('Vela hydration mismatch: invalid keyed row range reconciliation inputs');
  const next = new Map(); let index = 0;
  for (const item of iterable || []) {
    const key = keyOf(item, index);
    if (next.has(key)) throw new Error(`Vela keyed update has duplicate key ${String(key)}`);
    let row = blocks.get(key);
    if (row) { if (typeof update === 'function') update(row, item, index); }
    else row = create(item, index);
    $$moveHydrationKeyedRowRange(range.end, row);
    next.set(key, row); index++;
  }
  for (const [key, row] of blocks) if (!next.has(key)) { if (typeof row?.controller?.destroy === 'function') row.controller.destroy(); $$removeHydrationKeyedRowRange(row); }
  return next;
}

// $$clearHydrationKeyedRowMarkers removes only direct compiler element
// markers around adopted keyed rows. It is called immediately before the
// first keyed update, never during hydration, so initial SSR DOM remains
// untouched. Nested `v` markers belong to row text and are deliberately not
// traversed here.
export function $$clearHydrationKeyedRowMarkers(range) {
  if (!range?.start || !range?.end) {
    throw new Error('Vela hydration mismatch: invalid keyed row range');
  }
  for (let node = range.start.nextSibling; node && node !== range.end;) {
    const next = node.nextSibling;
    if (node.nodeType === 8 && /^(?:e:|\/e:)/.test(String(node.data || ''))) node.remove();
    node = next;
  }
}

// $$reconcileHydrationKeyedRows preserves existing row elements by key and
// places them in iterable order immediately before the structural end marker.
// Callers provide typed generated create/update callbacks; this runtime never
// evaluates template source or manufactures HTML from user expressions.
export function $$reconcileHydrationKeyedRows(range, blocks, iterable, keyOf, create, update) {
  if (!range?.end || !(blocks instanceof Map) || typeof keyOf !== 'function' || typeof create !== 'function') {
    throw new Error('Vela hydration mismatch: invalid keyed row reconciliation inputs');
  }
  // Moving an already-focused element within a parent makes Chromium clear
  // document.activeElement even though the keyed row itself is retained.
  // Preserve that native interaction state only for a retained old row; a
  // removed row must never be focused again after reconciliation.
  const doc = range.end.ownerDocument;
  const active = doc && doc.activeElement;
  const restoreFocus = active && Array.from(blocks.values()).some(row => row === active || row?.contains?.(active));
  const next = new Map();
  let index = 0;
  for (const item of iterable || []) {
    const key = keyOf(item, index);
    if (next.has(key)) throw new Error(`Vela keyed update has duplicate key ${String(key)}`);
    let row = blocks.get(key);
    if (row) {
      if (typeof update === 'function') update(row, item, index);
    } else {
      row = create(item, index);
    }
    if (!row || row.nodeType !== 1) throw new Error(`Vela keyed update cannot materialize key ${String(key)}`);
    range.end.before(row);
    next.set(key, row);
    index++;
  }
  for (const [key, row] of blocks) {
    if (!next.has(key)) row.remove();
  }
  if (restoreFocus && active.isConnected && typeof active.focus === 'function') active.focus();
  return next;
}

// $$reconcileHydrationKeyedElements is the marker-free counterpart of the
// row reconciler for a typed element collection already owned by one retained
// parent. It never evaluates template source: callers provide cached-AST
// key/create/update callbacks. Appending a retained element moves it into the
// requested order without replacing its identity.
export function $$reconcileHydrationKeyedElements(parent, blocks, iterable, keyOf, create, update) {
  if (!parent || parent.nodeType !== 1 || !(blocks instanceof Map) ||
      typeof keyOf !== 'function' || typeof create !== 'function') {
    throw new Error('Vela hydration mismatch: invalid keyed element reconciliation inputs');
  }
  const next = new Map();
  let index = 0;
  for (const item of iterable || []) {
    const key = keyOf(item, index);
    if (next.has(key)) throw new Error(`Vela keyed update has duplicate key ${String(key)}`);
    let element = blocks.get(key);
    if (element) {
      if (typeof update === 'function') update(element, item, index);
    } else {
      element = create(item, index);
    }
    if (!element || element.nodeType !== 1) {
      throw new Error(`Vela keyed update cannot materialize key ${String(key)}`);
    }
    parent.append(element);
    next.set(key, element);
    index++;
  }
  for (const [key, element] of blocks) {
    if (!next.has(key)) element.remove();
  }
  return next;
}

// $$writeHydrationText updates exactly one text range after hydration. The
// initial server text is left untouched; an empty server value gets a text
// node only when a later reactive update first becomes non-empty.
export function $$writeHydrationText(range, value) {
  if (!range || !range.start || !range.end) {
    throw new Error('Vela hydration mismatch: invalid text range');
  }
  const next = String(value);
  const first = range.start.nextSibling;
  if (first === range.end) {
    if (next === '') return;
    const parent = range.end.parentNode;
    const doc = range.end.ownerDocument || (parent && parent.ownerDocument);
    if (!parent || !doc || typeof doc.createTextNode !== 'function') {
      throw new Error('Vela hydration mismatch: cannot materialize text range');
    }
    parent.insertBefore(doc.createTextNode(next), range.end);
    return;
  }
  if (first.nodeType !== 3 || first.nextSibling !== range.end) {
    throw new Error('Vela hydration mismatch: text range contains unexpected DOM nodes');
  }
  if (first.data !== next) first.data = next;
}

// $$hydrationPayloadVersion is the schema understood by this browser runtime.
// Generated Go renderers include this value in every payload. Missing versions
// are tolerated only for pre-versioned local fixtures during the migration.
export const $$hydrationPayloadVersion = 1;

// Hydration is transactional because a later marker mismatch can happen after
// earlier listeners or child components have already been adopted. Resources
// register inverse operations as they are acquired. The root entry commits on
// success or rolls them back in reverse ownership order on failure; nested
// component entries share the same transaction.
export function $$createHydrationTransaction() {
  let state = 'active';
  const cleanups = [];
  return {
    add(cleanup) {
      if (state !== 'active' || typeof cleanup !== 'function') return false;
      cleanups.push(cleanup);
      return true;
    },
    commit() {
      if (state !== 'active') return false;
      state = 'committed';
      cleanups.length = 0;
      return true;
    },
    rollback() {
      if (state !== 'active') return [];
      state = 'rolled-back';
      const errors = [];
      for (let index = cleanups.length - 1; index >= 0; index--) {
        try { cleanups[index](); }
        catch (error) { errors.push(error); }
      }
      cleanups.length = 0;
      return errors;
    },
    get state() { return state; },
  };
}

export function $$addHydrationListener(transaction, target, event, handler) {
  target.addEventListener(event, handler);
  let attached = true;
  const remove = () => {
    if (!attached) return;
    attached = false;
    target.removeEventListener(event, handler);
  };
  if (transaction) transaction.add(remove);
  return remove;
}

// $$readHydrationPayload reads the inert server payload and verifies that it
// belongs to this component. Incomplete payloads are explicitly rejected: a
// future hydrator must not silently recreate state that SSR could not encode.
export function $$readHydrationPayload(root, componentId) {
  const label = componentId || 'unknown component';
  const doc = root && (root.ownerDocument || (root.nodeType === 9 ? root : null));
  const payloadNode = doc && typeof doc.getElementById === 'function'
    ? doc.getElementById(`vela-state-${componentId}`) : null;
  if (!payloadNode) {
    throw new Error(`Vela hydration mismatch [${label}]: missing state payload`);
  }
  if (payloadNode.type !== 'application/json') {
    throw new Error(`Vela hydration mismatch [${label}]: state payload is not application/json`);
  }
  let payload;
  try {
    payload = JSON.parse(payloadNode.textContent || '');
  } catch {
    throw new Error(`Vela hydration mismatch [${label}]: state payload is not valid JSON`);
  }
  if (!payload || payload.component !== componentId) {
    throw new Error(`Vela hydration mismatch [${label}]: state payload belongs to another component`);
  }
  if (payload.version !== undefined && payload.version !== $$hydrationPayloadVersion) {
    throw new Error(`Vela hydration mismatch [${label}]: unsupported state payload version ${String(payload.version)}`);
  }
  if (payload.incomplete) {
    const omitted = Array.isArray(payload.omitted) && payload.omitted.length
      ? `; omitted state: ${payload.omitted.join(', ')}` : '';
    throw new Error(`Vela hydration mismatch [${label}]: state payload is incomplete${omitted}`);
  }
  return payload;
}
