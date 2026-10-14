<script>
  import IssuesPage from '#lib/components/IssuesPage.svelte';
  let { data } = $props();
</script>

<svelte:head><title>Issues · Tracker</title></svelte:head>
<IssuesPage {...data} />
