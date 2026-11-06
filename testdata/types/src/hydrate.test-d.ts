// @govela/runtime/hydrate (TS-6.1, TS-6.3).
import { hydrate, preloadHydration, serverComponent, staticHTML, MISMATCH, type HydrateOptions, type MismatchReport } from '@govela/runtime/hydrate';
import { type Component, type ComponentInstance } from '@govela/runtime';
import { expectTrue, expectType, type Equal } from './expect.ts';

declare const Page: Component<{ title: string }, { saved: number }>;
declare const container: HTMLElement;

expectTrue<Equal<typeof MISMATCH, 'VELA-HYDRATION-MISMATCH'>>();
await preloadHydration(container);
const page = hydrate(Page, container, {
  policy: 'fail',
  onMismatch(report) {
    expectType<MismatchReport>(report);
    expectType<'repair' | 'fail' | 'production'>(report.policy);
  },
});
expectTrue<Equal<typeof page, ComponentInstance<{ title: string }>>>();
hydrate(Page, container);
// @ts-expect-error an unknown policy
hydrate(Page, container, { policy: 'ignore' });
const options: HydrateOptions = {};
hydrate(Page, container, options);

const adopted = serverComponent(Page, '<p>…</p>');
expectTrue<Equal<typeof adopted, Component<{ title: string }, { saved: number }, Record<string, any>>>>();
const fragment = staticHTML('<p>…</p>');
expectType<Component>(fragment);
// @ts-expect-error html is a string
staticHTML(1);
