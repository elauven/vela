// Types of @govela/app/client (docs/APP.md).

import type { Component, ComponentInstance } from '@govela/runtime';
import type { MismatchReport } from '@govela/runtime/hydrate';

/** The route table of src/_app/routes.js, which vela-app generates. */
export interface RouteTable {
  routes: { id: string; chain: { id: string; error?: string }[]; render?: 'auto' | 'data' | 'fragment' | 'document' }[];
  components: Record<string, { load(): Promise<unknown> }>;
  match(path: string): { route: { id: string }; params: Record<string, string> } | null;
}

/**
 * The route prop of route components (R-1). P are the parameters of the
 * path: `Route<Params>` of the route's `$types` (docs/TYPESCRIPT.md TS-8.2).
 */
export interface Route<P extends Record<string, string | undefined> = Record<string, string>> {
  id: string;
  url: string;
  path: string;
  params: P;
  query: Record<string, string>;
}

/** One end of a navigation: the URL and its route id (null when no route matches). */
export interface NavigationTarget {
  url: URL;
  route: string | null;
}

export type NavigationType = 'link' | 'goto' | 'popstate' | 'invalidate' | 'form';

export interface Navigation {
  from: NavigationTarget;
  to: NavigationTarget;
  type: NavigationType;
}

export interface BeforeNavigation extends Navigation {
  /** Keeps the current page. */
  cancel(): void;
}

export interface StartOptions {
  /** The generated app root, src/_app/VelaApp.vela. */
  VelaApp: Component<any, any, any>;
  /** The generated route table, src/_app/routes.js. Without it links load documents. */
  routes?: RouteTable;
  /** The element that holds the app root, or its id. */
  root?: string | Element;
  /** Receives hydration mismatch reports. */
  onMismatch?: (report: MismatchReport) => void;
  /** The default prefetch of links; data-vela-preload on a link or an ancestor overrides it. */
  preload?: 'hover' | 'viewport' | 'off';
}

/** Hydrates the server document and installs the router. */
export function start(options: StartOptions): Promise<{ app: ComponentInstance; routes?: RouteTable }>;

export interface GotoOptions {
  /** Replace the current history entry instead of adding one. */
  replace?: boolean;
  /** Kept in history.state.state. */
  state?: unknown;
  /** Run every loader again. */
  invalidate?: boolean;
}

/** Navigates like a link click; resolves when the navigation ends. */
export function goto(url: string | URL, options?: GotoOptions): Promise<void>;

/** Loads the current URL again with every loader, keeping scroll and focus. */
export function invalidate(): Promise<void>;

/** null, or the navigation that is loading. */
export const navigating: {
  subscribe(run: (value: Navigation | null) => void): () => void;
};

/** Calls fn before every navigation of the router; returns a function that removes it. */
export function beforeNavigate(fn: (navigation: BeforeNavigation) => void): () => void;

/** Calls fn after every navigation of the router; returns a function that removes it. */
export function afterNavigate(fn: (navigation: Navigation) => void): () => void;

/** The navigation protocol version (N-1). */
export const protocolVersion: 1;

/** The request headers of a navigation (N-2). */
export const headers: Readonly<{ navigate: string; have: string; build: string }>;

/** The detail of `vela:form`: a failed form action (`app.Fail`). */
export interface FormFailure {
  /** The HTTP status of the failure, 400–499. */
  status: number;
  /** The value the action failed with: the page's `form` prop. */
  form: unknown;
}

declare global {
  interface HTMLElementEventMap {
    /** The app root hydrated (start()). */
    'vela:hydrated': CustomEvent<{ app: ComponentInstance }>;
    /** A navigation of the router ended. */
    'vela:navigated': CustomEvent<Navigation>;
    /** A form action failed; the page's `form` prop changed. */
    'vela:form': CustomEvent<FormFailure>;
  }
}
