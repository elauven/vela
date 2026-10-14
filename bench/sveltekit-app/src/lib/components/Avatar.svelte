<script>
  let { user = null } = $props();
</script>

{#if user}
  <span class="avatar" style:background={user.color} title={user.name}>{user.initials}</span>
{/if}
