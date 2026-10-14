<script>
  import Nav from './Nav.svelte';
  import Card from './Card.svelte';

  let { project, columns, assignees, live = true } = $props();

  // The board is copied once from the server data and then changed locally.
  // svelte-ignore state_referenced_locally
  let board = $state(columns);
  let query = $state('');
  let assignee = $state('');
  let picked = $state(0);
  let over = $state('');
  let error = $state('');

  let shown = $derived(board.map((col) => ({
    status: col.status,
    title: col.title,
    total: col.cards.length,
    cards: col.cards.filter((card) => visible(card, query, assignee)),
  })));

  function visible(card, text, who) {
    if (who !== '' && (card.assignee === null || card.assignee.name !== who)) return false;
    return text === '' || card.title.toLowerCase().includes(text.toLowerCase());
  }

  function allow(event, status) {
    event.preventDefault();
    over = status;
  }

  async function drop(event, status) {
    event.preventDefault();
    over = '';
    const id = picked;
    picked = 0;
    const from = board.find((col) => col.cards.some((c) => c.id === id));
    if (!from || from.status === status) return;
    const card = from.cards.find((c) => c.id === id);
    const before = board;
    board = board.map((col) => {
      if (col.status === from.status) return { ...col, cards: col.cards.filter((c) => c.id !== id) };
      if (col.status === status) return { ...col, cards: [...col.cards, { ...card, status }] };
      return col;
    });
    if (!live) return;
    try {
      const res = await fetch(`/api/issues/${id}`, {
        method: 'PATCH',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ status }),
      });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      error = '';
    } catch (err) {
      board = before;
      error = `Could not move ${card.key}: ${err.message}`;
    }
  }
</script>

<div>
  <Nav current="board" />
  <main>
    <h1>{project.name} <span class="muted">{project.key}</span></h1>
    <div class="toolbar">
      <input type="search" placeholder="Filter cards" bind:value={query} />
      <select bind:value={assignee}>
        <option value="">Everyone</option>
        {#each assignees as name (name)}
          <option value={name}>{name}</option>
        {/each}
      </select>
      {#if error}
        <span class="error">{error}</span>
      {/if}
    </div>
    <div class="board">
      {#each shown as col (col.status)}
        <section
          class="column"
          class:over={over === col.status}
          role="list"
          ondragover={(e) => allow(e, col.status)}
          ondragleave={() => (over = '')}
          ondrop={(e) => drop(e, col.status)}
        >
          <header>
            <h2>{col.title}</h2>
            <span class="muted">{col.cards.length}/{col.total}</span>
          </header>
          {#each col.cards as card (card.id)}
            <Card {card} dragging={picked === card.id} onpick={(id) => (picked = id)} />
          {/each}
        </section>
      {/each}
    </div>
  </main>
</div>
