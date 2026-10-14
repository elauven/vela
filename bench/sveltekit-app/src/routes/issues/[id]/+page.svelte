<script>
  import IssuePage from '#lib/components/IssuePage.svelte';
  let { data } = $props();
</script>

<svelte:head><title>Issue · Tracker</title></svelte:head>
<IssuePage {...data} />
