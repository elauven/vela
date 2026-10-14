<script>
  import { replaceState } from '$app/navigation';
  import Nav from './Nav.svelte';
  import Avatar from './Avatar.svelte';

  let { projects, filters, rows, total, pages } = $props();

  const STATUSES = [
    { status: 'backlog', title: 'Backlog' },
    { status: 'todo', title: 'To do' },
    { status: 'in_progress', title: 'In progress' },
    { status: 'done', title: 'Done' },
  ];
  const SORTS = [
    { sort: 'updated', title: 'Recently updated' },
    { sort: 'priority', title: 'Priority' },
    { sort: 'number', title: 'Number' },
  ];

  // svelte-ignore state_referenced_locally
  let project = $state(filters.project);
  // svelte-ignore state_referenced_locally
  let status = $state(filters.status);
  // svelte-ignore state_referenced_locally
  let q = $state(filters.q);
  // svelte-ignore state_referenced_locally
  let sort = $state(filters.sort);
  // svelte-ignore state_referenced_locally
  let page = $state(filters.page);
  // svelte-ignore state_referenced_locally
  let list = $state(rows);
  // svelte-ignore state_referenced_locally
  let count = $state(total);
  // svelte-ignore state_referenced_locally
  let pageCount = $state(pages);
  let loading = $state(false);
  const pending = { timer: 0, request: 0 };

  async function load() {
    const qs = `project=${project}&status=${status}&q=${encodeURIComponent(q)}&sort=${sort}&page=${page}`;
    const request = ++pending.request;
    loading = true;
    const res = await fetch(`/api/issues?${qs}`);
    const data = await res.json();
    if (request !== pending.request) return;
    list = data.rows;
    count = data.total;
    pageCount = data.pages;
    loading = false;
    replaceState(`/issues?${qs}`, {});
  }

  function choose(field, event) {
    if (field === 'project') project = event.target.value;
    if (field === 'status') status = event.target.value;
    if (field === 'sort') sort = event.target.value;
    page = 1;
    load();
  }

  function typed(event) {
    q = event.target.value;
    clearTimeout(pending.timer);
    pending.timer = setTimeout(() => {
      page = 1;
      load();
    }, 200);
  }

  function go(delta) {
    page = page + delta;
    load();
  }
</script>

<div>
  <Nav current="issues" />
  <main>
    <h1>Issues <span class="muted">{count}</span></h1>
    <div class="toolbar">
      <select onchange={(e) => choose('project', e)}>
        {#each projects as p (p.id)}
          <option value={String(p.id)} selected={String(p.id) === project}>{p.key} · {p.name}</option>
        {/each}
      </select>
      <select onchange={(e) => choose('status', e)}>
        <option value="" selected={status === ''}>All statuses</option>
        {#each STATUSES as s (s.status)}
          <option value={s.status} selected={s.status === status}>{s.title}</option>
        {/each}
      </select>
      <input type="search" placeholder="Search titles" value={q} oninput={typed} />
      <select onchange={(e) => choose('sort', e)}>
        {#each SORTS as s (s.sort)}
          <option value={s.sort} selected={s.sort === sort}>{s.title}</option>
        {/each}
      </select>
    </div>
    <table class:loading>
      <thead>
        <tr><th>Key</th><th>Title</th><th>Status</th><th>Priority</th><th>Assignee</th><th>Updated</th><th>Comments</th></tr>
      </thead>
      <tbody>
        {#each list as row (row.id)}
          <tr>
            <td class="muted">{row.key}</td>
            <td><a href={`/issues/${row.id}`}>{row.title}</a></td>
            <td><span class="status">{row.statusTitle}</span></td>
            <td><span class={`priority p${row.priority}`}></span></td>
            <td><Avatar user={row.assignee} /></td>
            <td>{row.updated}</td>
            <td>{row.comments}</td>
          </tr>
        {:else}
          <tr><td colspan="7" class="muted">No issues match.</td></tr>
        {/each}
      </tbody>
    </table>
    <div class="pager">
      <button disabled={page <= 1 || loading} onclick={() => go(-1)}>Previous</button>
      <span>Page {page} of {pageCount}</span>
      <button disabled={page >= pageCount || loading} onclick={() => go(1)}>Next</button>
    </div>
  </main>
</div>
