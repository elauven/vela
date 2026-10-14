<script>
  import Avatar from './Avatar.svelte';

  let { card, dragging = false, onpick } = $props();

  function start(event) {
    event.dataTransfer.effectAllowed = 'move';
    event.dataTransfer.setData('text/plain', String(card.id));
    onpick?.(card.id);
  }
</script>

<article class="card" class:dragging draggable="true" ondragstart={start}>
  <span class="key">{card.key}</span>
  <a class="title" href={`/issues/${card.id}`}>{card.title}</a>
  <div>
    {#each card.labels as label (label.name)}
      <span class="label" style:background={label.color}>{label.name}</span>
    {/each}
  </div>
  <footer>
    <span class={`priority p${card.priority}`}></span>
    {#if card.due}
      <span>{card.due}</span>
    {/if}
    <span class="spacer"></span>
    {#if card.comments}
      <span>💬 {card.comments}</span>
    {/if}
    {#if card.attachments}
      <span>📎 {card.attachments}</span>
    {/if}
    <Avatar user={card.assignee} />
  </footer>
</article>
