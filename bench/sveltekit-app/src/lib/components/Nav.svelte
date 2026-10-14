<script>
  let { current = '' } = $props();
</script>

<nav class="nav">
  <a class="brand" href="/">Tracker</a>
  <a href="/projects/1/board" class:active={current === 'board'}>Board</a>
  <a href="/issues" class:active={current === 'issues'}>Issues</a>
  <a href="/dashboard" class:active={current === 'dashboard'}>Dashboard</a>
</nav>
