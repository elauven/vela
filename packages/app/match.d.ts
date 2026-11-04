// Types of @govela/app/match for editors (docs/APP.md P-6, P-8, P-13).

export type Matcher = (segment: string) => boolean;

export const int: Matcher;
export const uuid: Matcher;
export const slug: Matcher;
export const builtinMatchers: Readonly<{ int: Matcher; uuid: Matcher; slug: Matcher }>;

export interface PatternSegment {
  kind: 'static' | 'param' | 'rest';
  value: string;
  matcher?: string;
}

export interface MatchTable<R> {
  routes: R[];
  patterns: { route: number; segments: PatternSegment[] }[];
  matchers: Record<string, Matcher>;
}

/** Finds the route of a path: the first pattern that matches in full. */
export function matchPath<R>(table: MatchTable<R>, path: string): { route: R; params: Record<string, string> } | null;
