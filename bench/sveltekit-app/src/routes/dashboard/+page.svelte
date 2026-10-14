<script>
  import DashboardPage from '#lib/components/DashboardPage.svelte';
  let { data } = $props();
</script>

<svelte:head><title>Dashboard · Tracker</title></svelte:head>
<DashboardPage {...data} />
