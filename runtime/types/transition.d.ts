// Fragment: the transitions and animations of `vela/transition` and
// `vela/animate` (also exported by `vela` and `@govela/runtime`), task 096.

/** An easing function: progress 0..1 to eased progress. */
export type EasingFunction = (t: number) => number;

/** What a transition or an animation returns: css(t, u) is the inline CSS at t (0 hidden, 1 shown; u = 1 - t). */
export interface TransitionConfig {
  delay?: number;
  duration?: number;
  easing?: EasingFunction;
  css?: (t: number, u: number) => string;
}

/** A transition for transition:, in: and out:. */
export type Transition<E extends Element = Element, P = any> = (node: E, params: P, options: { direction: 'in' | 'out' }) => TransitionConfig;

/** An animation for animate: in a keyed {#each}. */
export type Animate<E extends Element = Element, P = any> = (node: E, positions: { from: DOMRect; to: DOMRect }, params: P) => TransitionConfig;

export interface FadeParams {
  delay?: number;
  duration?: number;
  easing?: EasingFunction;
}

export interface FlyParams extends FadeParams {
  x?: number | string;
  y?: number | string;
  opacity?: number;
}

export interface ScaleParams extends FadeParams {
  start?: number;
  opacity?: number;
}

export interface SlideParams extends FadeParams {
  axis?: 'x' | 'y';
}

export interface FlipParams {
  delay?: number;
  duration?: number | ((distance: number) => number);
  easing?: EasingFunction;
}

export function linear(t: number): number;
export function cubicOut(t: number): number;
export function cubicInOut(t: number): number;
export function fade(node: Element, params?: FadeParams): TransitionConfig;
export function fly(node: Element, params?: FlyParams): TransitionConfig;
export function scale(node: Element, params?: ScaleParams): TransitionConfig;
export function slide(node: Element, params?: SlideParams): TransitionConfig;
export function flip(node: Element, positions: { from: DOMRect; to: DOMRect }, params?: FlipParams): TransitionConfig;
