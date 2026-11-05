// @govela/app/client and @govela/app/match (docs/APP.md).
import { start, goto, invalidate, navigating, beforeNavigate, afterNavigate, headers, protocolVersion, type Navigation, type FormFailure } from '@govela/app/client';
import { matchPath, int, uuid, slug, builtinMatchers, type MatchTable } from '@govela/app/match';
import { type Component, type ComponentInstance } from '@govela/runtime';
import { expectTrue, expectType, type Equal } from './expect.ts';

declare const VelaApp: Component<{ route: string }>;
const { app } = await start({ VelaApp, root: 'vela-app', preload: 'viewport', onMismatch: (report) => expectType<string>(report.message) });
expectType<ComponentInstance>(app);
// @ts-expect-error VelaApp is a component
start({ VelaApp: 'App' });
// @ts-expect-error an unknown preload mode
start({ VelaApp, preload: 'always' });

await goto('/users', { replace: true, state: { from: 'test' } });
await goto(new URL('https://example.com/a'));
await invalidate();
const stop = navigating.subscribe((value) => {
  if (value) expectType<'link' | 'goto' | 'popstate' | 'invalidate' | 'form'>(value.type);
});
stop();
beforeNavigate((navigation) => {
  if (navigation.to.route === null) navigation.cancel();
});
afterNavigate((navigation) => expectType<Navigation>(navigation));
expectTrue<Equal<typeof protocolVersion, 1>>();
expectType<string>(headers.navigate);

const root = document.getElementById('vela-app')!;
root.addEventListener('vela:form', (event) => {
  expectType<FormFailure>(event.detail);
  expectType<number>(event.detail.status);
});
root.addEventListener('vela:navigated', (event) => expectType<Navigation>(event.detail));
root.addEventListener('vela:hydrated', (event) => expectType<ComponentInstance>(event.detail.app));

const table: MatchTable<{ id: string }> = { routes: [{ id: 'a' }], patterns: [], matchers: builtinMatchers };
const found = matchPath(table, '/a');
if (found) expectType<{ id: string }>(found.route);
expectType<boolean>(int('1') && uuid('x') && slug('y'));
