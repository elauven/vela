package typecheck

// RenderHelpers declares the helpers of the render function of a virtual
// document (document.go, template.go). The helper process declares them
// with the ambient types of the runtime package (runtime/types), unless
// the project's tsconfig lists those itself (TS-10.2).
const RenderHelpers = `
declare const $$target: Element;
declare function $$each<T>(items: Iterable<T> | ArrayLike<T> | null | undefined): Iterable<T>;
declare function $$awaited<T>(promise: T): Awaited<T>;
declare const $$error: any;
declare const $$retry: () => void;
declare function $$on<K extends keyof HTMLElementEventMap>(event: K, handler: (event: HTMLElementEventMap[K]) => any): void;
declare function $$on(event: string, handler: (event: Event) => any): void;

/** The element type of a tag name, for use: (task 093). */
type $$VelaElement<T extends string> = T extends keyof HTMLElementTagNameMap ? HTMLElementTagNameMap[T] : T extends keyof SVGElementTagNameMap ? SVGElementTagNameMap[T] : HTMLElement;
declare function $$element<T extends string>(tag: T): $$VelaElement<T>;
/** use:action={params}: the action takes the element and the parameters. */
declare function $$action<E extends Element, P>(action: (node: E, params: P) => any, node: E, params: P): void;
/** use:action without a value: the action takes no parameters. */
declare function $$action<E extends Element>(action: (node: E, params?: undefined) => any, node: E): void;
/** transition:, in:, out: (task 096): the transition takes the element and the parameters and returns its configuration. */
declare function $$transition<E extends Element, P>(transition: (node: E, params: P, options: { direction: 'in' | 'out' }) => import('vela').TransitionConfig, node: E, params: P): void;
declare function $$transition<E extends Element>(transition: (node: E, params?: undefined, options?: { direction: 'in' | 'out' }) => import('vela').TransitionConfig, node: E): void;
/** animate: in a keyed {#each} row (task 096). */
declare function $$animate<E extends Element, P>(animation: (node: E, positions: { from: DOMRect; to: DOMRect }, params: P) => import('vela').TransitionConfig, node: E, params: P): void;
declare function $$animate<E extends Element>(animation: (node: E, positions: { from: DOMRect; to: DOMRect }, params?: undefined) => import('vela').TransitionConfig, node: E): void;

/** The component of a value of <vela:component :this>, a Lazy one loaded. */
type $$VelaUnlazy<C> = C extends import('vela').Lazy<infer L> ? L : C;
type $$VelaCandidates<C> = $$VelaUnlazy<Exclude<C, null | undefined | false>>;
declare function $$dynamicValue<C extends import('vela').Component<any, any, any, any> | import('vela').Lazy<any> | ((...args: any[]) => any) | null | undefined | false>(component: C): C;

/** The prop names of every candidate; any for a component of unknown props. */
type $$VelaPropKeys<C> = C extends import('vela').Component<infer P, any, any, any> ? keyof P : PropertyKey;
/** <vela:component>: a prop no candidate declares is an error (TS-4.10). */
declare function $$dynamic<C, const Q extends object>(component: C, props: Q & { [K in Exclude<keyof Q, $$VelaPropKeys<$$VelaCandidates<C>>>]: never }): void;

type $$VelaEventMap<D> = D extends { readonly $$events_def?: infer E } ? NonNullable<E> : Record<string, any>;
type $$VelaEventName<C> = $$VelaCandidates<C> extends infer D ? D extends any ? (string extends keyof $$VelaEventMap<D> ? string : keyof $$VelaEventMap<D> & string) : never : never;
type $$VelaEventDetail<C, K> = $$VelaCandidates<C> extends infer D ? D extends any ? (K extends keyof $$VelaEventMap<D> ? $$VelaEventMap<D>[K] : any) : never : never;
/** @on:name of a component tag: the name is one of its events, the handler gets its detail (TS-4.6). */
declare function $$event<C, K extends $$VelaEventName<C>>(component: C, name: K, handler: (event: CustomEvent<$$VelaEventDetail<C, K>>) => any): void;

type $$VelaBindName<D> = D extends { readonly $$bindings_def?: infer B } ? (string extends B ? string : B & string) : string;
type $$VelaBindEach<C> = C extends any ? (name: $$VelaBindName<C>) => void : never;
/** The props every candidate lets a parent bind. */
type $$VelaBindAll<C> = $$VelaBindEach<$$VelaCandidates<C>> extends (name: infer N) => void ? N : never;
/** bind:name of a component tag: a $bindable prop (of every candidate). */
declare function $$bind<C, K extends $$VelaBindAll<C>>(component: C, name: K): void;

type $$VelaSlotName<D> = D extends { readonly $$slot_def?: infer S } ? (string extends keyof NonNullable<S> ? string : keyof NonNullable<S> & string) : string;
/** slot="name" of a child: a slot of the component. */
declare function $$slot<C, K extends $$VelaSlotName<$$VelaCandidates<C>>>(component: C, name: K): void;
`
