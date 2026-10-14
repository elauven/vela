<script>
  import { afterNavigate } from '$app/navigation';

  let { children } = $props();

  // The end of hydration (the first navigation), for shared/browser-metrics.mjs.
  afterNavigate(() => performance.mark('app-hydrated'));
</script>

{@render children()}
