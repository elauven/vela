<script>
  import BoardPage from '#lib/components/BoardPage.svelte';
  let { data } = $props();
</script>

<svelte:head><title>Board · Tracker</title></svelte:head>
<BoardPage {...data} />
