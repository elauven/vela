// Browser check of examples/app for TestExampleAppHydrates (tasks 075,
// 076).
//
// Usage: node app/testdata/e2e.mjs <project> <server binary> <cases JSON>
//
// Builds the client with vite build (the project's vite.config.js), starts
// the Go server with the build, opens every case in Chromium, then runs the
// navigation scenarios of task 076. Prints {"pages": [{path, status, title,
// location, hydrated, sameDOM, removed, preloaded, after, logs, diff}],
// "navigation": [{name, error}]} or {"error": "..."}.
import { spawn } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { createInterface } from 'node:readline';
import { chromium } from 'playwright';
import { build } from 'vite';

const [project, serverBinary, casesJSON] = process.argv.slice(2);
const cases = JSON.parse(casesJSON);

function startServer(addr = '127.0.0.1:0', extra = []) {
  // Plain HTTP: session cookies without Secure (docs/APP.md G-4).
  const child = spawn(serverBinary, ['-addr', addr, '-dist', 'dist', '-insecure-cookies', ...extra], { cwd: project, stdio: ['ignore', 'pipe', 'pipe'] });
  let stderr = '';
  child.stderr.on('data', (chunk) => { stderr += chunk; });
  return new Promise((resolve, reject) => {
    createInterface({ input: child.stdout }).once('line', (url) => resolve({
      url,
      stop: () => new Promise((done) => {
        child.once('exit', done);
        child.kill();
      }),
    }));
    child.on('exit', (code) => reject(new Error(`server exited (${code}): ${stderr}`)));
  });
}

async function check(browser, url, c, files) {
  const response = await fetch(url + c.path, { redirect: 'manual' });
  const result = { path: c.path, status: response.status, location: response.headers.get('location') ?? '', logs: [] };
  if (c.location) return result;
  const serverHTML = await response.text();
  const page = await browser.newPage();
  page.on('console', (m) => {
    if (m.type() !== 'error' && m.type() !== 'warning') return;
    // Chromium reports the status of an error document itself.
    if (m.location().url === url + c.path && m.text().startsWith('Failed to load resource')) return;
    result.logs.push(`${m.type()}: ${m.text()} (${m.location().url})`);
  });
  page.on('pageerror', (e) => result.logs.push(`pageerror: ${e.message}`));
  // An element of the server HTML removed during hydration means the
  // client did not adopt it; hydrate removes only the payload script.
  await page.addInitScript(() => {
    window.__removed = [];
    new MutationObserver((records) => {
      for (const r of records) {
        for (const n of r.removedNodes) {
          if (n.nodeType === 1 && !n.hasAttribute('data-vela-root')) window.__removed.push(n.outerHTML);
        }
      }
    }).observe(document, { childList: true, subtree: true });
    document.addEventListener('vela:hydrated', () => { window.__hydrated = true; });
  });
  try {
    const navigation = await page.goto(url + c.path);
    result.status = navigation.status();
    result.hydrated = await page.waitForFunction(() => window.__hydrated === true, null, { timeout: 15000 }).then(() => true, () => false);
    Object.assign(result, await page.evaluate((html) => {
      // The DOM of the server HTML without the payload script, as the
      // hydrated page must still have it.
      const server = new DOMParser().parseFromString(html, 'text/html').getElementById('vela-app');
      server.querySelector('script[data-vela-root]')?.remove();
      const client = document.getElementById('vela-app').innerHTML;
      let at = 0;
      while (at < client.length && client[at] === server.innerHTML[at]) at++;
      return {
        title: document.title,
        removed: window.__removed,
        sameDOM: client === server.innerHTML,
        diff: client === server.innerHTML ? '' : `server: ${server.innerHTML.slice(Math.max(0, at - 80), at + 80)}\nclient: ${client.slice(Math.max(0, at - 80), at + 80)}`,
        preloads: [...document.querySelectorAll('link[rel=modulepreload]')].map((l) => new URL(l.href).pathname.slice(1)),
      };
    }, serverHTML));
    result.preloaded = result.preloads.map((file) => files[file]).filter(Boolean);
    delete result.preloads;
    if (c.click) {
      await page.click(c.click);
      await page.waitForTimeout(100);
      result.after = await page.textContent(c.click);
    }
  } finally {
    await page.close();
  }
  return result;
}

// The navigation scenarios of task 076 run in one page each, in order;
// each returns nothing or throws. They use the links of the example where
// it has them and add a link to the body otherwise.
const scenarios = [
  ['a navigation between users keeps the users layout', async (t) => {
    await t.open('/users/1');
    await t.page.fill('.filter', 'a');
    await t.page.evaluate(() => { window.__filter = document.querySelector('.filter'); });
    const before = await t.loads();
    await t.follow('aside a[href="/users/2"]');
    t.equal(await t.text('article h1'), 'Grace', 'page');
    t.equal(await t.page.title(), 'Grace · Users', 'title');
    t.equal(await t.page.getAttribute('meta[name=description]', 'content'), 'The profile of Grace', 'description');
    t.equal(await t.page.inputValue('.filter'), 'a', 'the filter of the layout');
    t.equal(await t.page.evaluate(() => document.querySelector('.filter') === window.__filter), true, 'the layout keeps its DOM');
    t.equal(await t.text('aside a.active'), 'Grace', 'the layout gets the new route');
    t.equal((await t.loads())['users/Layout'], before['users/Layout'], 'runs of the users layout loader');
    t.equal(t.last().start, 2, 'start');
    t.equal(t.last().segments.map((s) => s.id).join(), 'routes/users/[id=int]/Page', 'segments');
  }],
  ['a navigation below the root layout replaces the level below it', async (t) => {
    await t.open('/');
    await t.page.evaluate(() => { window.__site = document.querySelector('.site'); });
    await t.follow('nav a[href="/users"]');
    t.equal(await t.page.evaluate(() => document.querySelector('.site') === window.__site), true, 'the root layout keeps its DOM');
    t.equal(await t.text('nav a.active'), 'Users', 'the root layout gets the new route');
    t.equal(await t.page.title(), 'Users', 'title');
    t.equal(await t.page.evaluate(() => !!document.querySelector('.users aside')), true, 'the users layout');
    t.equal(t.last().start, 1, 'start');
    await t.follow('main ul a[href="/users/1"]');
    t.equal(await t.text('article h1'), 'Ada', 'page');
    t.equal(t.last().start, 2, 'start from /users to /users/1');
    t.equal(await t.text('nav a.active'), 'Users', 'the nested NavLink follows getPage() below the section');
  }],
  ['a component deep in the root layout follows getPage() on navigation', async (t) => {
    await t.open('/');
    t.equal(await t.text('nav a.active'), 'Home', 'the section after hydration');
    await t.follow('nav a[href="/about"]');
    t.equal(await t.text('nav a.active'), 'About', 'a route id with an optional parameter');
    await t.follow('nav a[href="/settings"]');
    t.equal(await t.text('nav a.active'), 'Settings', 'a route id with a group');
    await t.navigation(() => t.page.goBack());
    t.equal(await t.text('nav a.active'), 'About', 'back');
  }],
  ['back and forward restore the scroll position', async (t) => {
    await t.open('/users');
    await t.page.evaluate(() => {
      document.body.style.minHeight = '5000px';
      scrollTo(0, 1500);
    });
    // A click that does not scroll the link into view.
    await t.navigation(() => t.page.evaluate(() => document.querySelector('main ul a[href="/users/2"]').click()));
    t.equal(await t.page.evaluate(() => scrollY), 0, 'scroll after a link');
    await t.page.evaluate(() => scrollTo(0, 700));
    await t.navigation(() => t.page.goBack());
    t.equal(await t.page.evaluate(() => location.pathname), '/users', 'path after back');
    t.equal(await t.page.evaluate(() => scrollY), 1500, 'scroll after back');
    await t.navigation(() => t.page.goForward());
    t.equal(await t.page.evaluate(() => location.pathname), '/users/2', 'path after forward');
    t.equal(await t.page.evaluate(() => scrollY), 700, 'scroll after forward');
    t.equal(await t.text('article h1'), 'Grace', 'page after forward');
  }],
  ['a loader redirect navigates to its location', async (t) => {
    await t.open('/');
    await t.follow('nav a[href="/me"]');
    t.equal(await t.page.evaluate(() => location.pathname), '/users/1', 'path');
    t.equal(await t.text('article h1'), 'Ada', 'page');
  }],
  ['a 404 during a navigation renders Error.vela', async (t) => {
    await t.open('/users/1');
    await t.addLink('/users/9');
    await t.follow('#test-link');
    t.equal(await t.text('main h1'), '404', 'error page');
    t.equal(await t.page.title(), 'Vela App', 'title');
    t.equal(await t.page.evaluate(() => !!document.querySelector('.users aside')), true, 'the users layout stays');
    t.equal(t.last().segments.at(-1).id, 'routes/Error', 'error segment');
    await t.follow('aside a[href="/users/2"]');
    t.equal(await t.text('article h1'), 'Grace', 'page after the error');
  }],
  ['a URL without a client route loads the document', async (t) => {
    await t.open('/');
    await t.addLink('/nope');
    await Promise.all([t.page.waitForURL('**/nope'), t.page.click('#test-link')]);
    await t.page.waitForFunction(() => window.__hydrated === true);
    t.equal(await t.page.evaluate(() => window.__marker), undefined, 'a new document');
    t.equal(await t.text('main h1'), '404', 'error page');
  }],
  ['a form adds and deletes a user with the same Go action', async (t) => {
    await t.open('/users');
    // A failure sets the form prop of the page; nothing else changes.
    await t.formResult(() => t.page.click('form.add button'));
    t.equal(await t.text('form.add .error'), 'A name is required.', 'error');
    t.equal(await t.page.evaluate(() => window.__marker), 1, 'no document load');
    await t.page.fill('form.add input', 'Linus');
    await t.navigation(() => t.page.click('form.add button'));
    t.equal(t.last().type, 'data', 'success is data');
    t.equal(t.last().start, 0, 'every loader runs again');
    t.equal(await t.text('.user-list li:last-child a'), 'Linus', 'added');
    t.equal(await t.text('aside p'), '3 users', 'the layout ran again');
    t.equal(await t.page.evaluate(() => document.querySelector('form.add .error')), null, 'the form prop is gone');
    t.equal(await t.page.evaluate(() => location.pathname + location.search), '/users', 'URL');
    await t.navigation(() => t.page.click('button[aria-label="Delete Linus"]'));
    t.equal(await t.page.evaluate(() => [...document.querySelectorAll('.user-list li a')].map((a) => a.textContent).join()), 'Ada,Grace', 'deleted');
    t.equal(await t.text('aside p'), '2 users', 'the layout after delete');
    t.equal(await t.page.evaluate(() => location.pathname + location.search), '/users', 'URL after delete');
  }],
  ['a hovered link is prefetched, and its navigation makes no request', async (t) => {
    await t.open('/users');
    await t.page.hover('main ul a[href="/users/2"]');
    await t.page.waitForTimeout(400);
    const before = t.requests();
    t.equal(before > 0, true, 'hover prefetched');
    await t.follow('main ul a[href="/users/2"]');
    t.equal(t.requests(), before, 'navigation requests after the click');
    t.equal(await t.text('article h1'), 'Grace', 'page');
    // Off: no prefetch.
    await t.page.evaluate(() => document.querySelector('aside a[href="/users/1"]').setAttribute('data-vela-preload', 'off'));
    await t.page.hover('aside a[href="/users/1"]');
    await t.page.waitForTimeout(300);
    t.equal(t.requests(), before, 'no prefetch with data-vela-preload="off"');
  }],
  ['the (admin) guard sends to the sign-in page and back', async (t) => {
    await t.open('/');
    await t.follow('nav a[href="/settings"]');
    t.equal(await t.page.evaluate(() => location.pathname + location.search), '/login?next=%2Fsettings', 'the guard redirected the navigation');
    await t.page.fill('form.login input[name=name]', 'Ada');
    await t.navigation(() => t.page.click('form.login button'));
    t.equal(await t.text('main h1'), 'Settings', 'after signing in');
    t.equal(await t.text('.badge'), 'Admin', 'the admin layout');
  }],
  ['a prefetch before signing out is not shown after it', async (t) => {
    await t.signIn();
    await t.open('/');
    const before = t.requests();
    await t.page.hover('nav a[href="/settings"]');
    await t.page.waitForTimeout(400);
    t.equal(t.requests() > before, true, 'hover prefetched /settings');
    await t.follow('nav a[href="/login"]');
    await t.navigation(() => t.page.click('button.logout'));
    t.equal(await t.page.evaluate(() => location.pathname), '/', 'signed out, back home');
    const cookies = await t.page.context().cookies();
    t.equal(cookies.some((c) => c.name === 'session'), false, 'the session cookie is gone');
    await t.follow('nav a[href="/settings"]');
    t.equal(await t.page.evaluate(() => location.pathname), '/login', 'the click asked the server and got the guard\'s redirect');
  }],
  ['a redirect of middleware keeps the root layout', async (t) => {
    await t.open('/');
    await t.page.evaluate(() => { window.__site = document.querySelector('.site'); });
    await t.addLink('/users/0');
    await t.follow('#test-link');
    t.equal(await t.page.evaluate(() => location.pathname), '/users', 'followed app.WriteRedirect');
    t.equal(await t.page.evaluate(() => document.querySelector('.site') === window.__site), true, 'the root layout keeps its DOM');
  }],
  ['beforeNavigate keeps a page with unsaved changes', async (t) => {
    await t.signIn();
    await t.open('/');
    await t.follow('nav a[href="/settings"]');
    await t.page.fill('.name', 'Ada');
    t.page.once('dialog', (dialog) => dialog.dismiss());
    await t.page.click('nav a[href="/"]');
    await t.page.waitForTimeout(300);
    t.equal(await t.page.evaluate(() => location.pathname), '/settings', 'path after cancel');
    t.equal(await t.page.inputValue('.name'), 'Ada', 'the form keeps its value');
    t.page.once('dialog', (dialog) => dialog.accept());
    await t.follow('nav a[href="/"]');
    t.equal(await t.text('main h1'), 'Home', 'page after confirm');
    // The page is gone, and so is its hook: no dialog.
    await t.follow('nav a[href="/users"]');
    t.equal(await t.page.evaluate(() => location.pathname), '/users', 'path');
  }],
  ['a static section navigates with HTML fragments and loads no JavaScript', async (t) => {
    await t.open('/');
    const scripts = t.scripts();
    await t.follow('nav a[href="/docs/intro"]');
    t.equal(t.last().type, 'fragment', 'response type');
    t.equal(t.last().fragment.static, true, 'static fragment');
    t.equal(t.last().start, 1, 'start');
    t.equal(await t.text('article.doc h1'), 'Introduction', 'page');
    t.equal(await t.page.title(), 'Introduction · Docs', 'title');
    await t.follow('.toc a[href="/docs/routing"]');
    t.equal(t.last().type, 'fragment', 'second response type');
    // The static levels hold no data: the client asks from their first level.
    t.equal(t.last().start, 1, 'second start');
    t.equal(await t.text('article.doc h1'), 'Routing', 'second page');
    t.equal(await t.text('.toc a.active'), 'Routing', 'the fragment has the new route');
    t.equal(scripts().length, 0, `scripts loaded (${scripts().join(', ')})`);
    // Leaving the docs: a data level replaces the static ones.
    await t.follow('nav a[href="/users"]');
    t.equal(t.last().type, 'data', 'leaving type');
    t.equal(await t.text('main ul a[href="/users/1"]'), 'Ada', 'users page');
  }],
  ['an interactive fragment hydrates, and data after it updates its instance', async (t) => {
    await t.open('/');
    await t.follow('nav a[href="/reports"]');
    t.equal(t.last().type, 'data', '/reports is a data route');
    await t.follow('.report-list a[href="/reports/1"]');
    t.equal(t.last().type, 'fragment', 'response type');
    t.equal(t.last().fragment.static, false, 'interactive fragment');
    t.equal(t.last().start, 2, 'start');
    t.equal(await t.text('.report h1'), 'Sales', 'report');
    t.equal(await t.text('.report li'), 'South: 10', 'first row ascending');
    await t.page.click('button.sort');
    await t.page.waitForTimeout(50);
    t.equal(await t.text('.report li'), 'North: 30', 'first row after sort');
    t.equal(await t.text('button.sort'), 'Descending', 'button after sort');
  }],
  ['navigations inside and below a fragment root update the right instance', async (t) => {
    await t.open('/');
    // The reports layout and the report come as one fragment from level 1.
    await t.follow('nav a[href="/reports"]');
    await t.page.evaluate(() => document.querySelector('#test-link')?.remove());
    await t.addLink('/reports/1');
    await t.follow('#test-link');
    t.equal(t.last().type, 'fragment', 'from / to /reports/1');
    t.equal(t.last().start, 2, 'start from /reports');
    // Load the report straight under the root layout instead.
    await t.open('/');
    await t.addLink('/reports/1');
    await t.follow('#test-link');
    t.equal(t.last().start, 1, 'start from /');
    t.equal(t.last().type, 'fragment', 'fragment from /');
    await t.page.click('button.pin');
    await t.page.evaluate(() => { window.__header = document.querySelector('.reports header'); });
    // Data at level 2 goes to the fragment's instance, which keeps the
    // layout and its state.
    await t.follow('.reports header a[href="/reports"]');
    t.equal(t.last().type, 'data', 'to /reports');
    t.equal(await t.text('.report-list a[href="/reports/2"]'), 'Visits', 'the list');
    t.equal(await t.text('button.pin'), 'Pinned 1', 'layout state after data');
    t.equal(await t.page.evaluate(() => document.querySelector('.reports header') === window.__header), true, 'layout DOM after data');
    // A fragment at level 2 nests inside the fragment's instance.
    await t.follow('.report-list a[href="/reports/2"]');
    t.equal(t.last().type, 'fragment', 'to /reports/2');
    t.equal(t.last().start, 2, 'start of the nested fragment');
    t.equal(await t.text('.report h1'), 'Visits', 'nested report');
    t.equal(await t.text('button.pin'), 'Pinned 1', 'layout state after a nested fragment');
    await t.page.click('button.sort');
    await t.page.waitForTimeout(50);
    t.equal(await t.text('button.sort'), 'Descending', 'the nested fragment is interactive');
    // Back across data and fragment levels.
    await t.navigation(() => t.page.goBack());
    t.equal(await t.text('.report-list a[href="/reports/1"]'), 'Sales', 'back to the list');
    t.equal(await t.text('button.pin'), 'Pinned 1', 'layout state after back');
    await t.navigation(() => t.page.goBack());
    t.equal(await t.text('.report h1'), 'Sales', 'back to the first report');
    await t.navigation(() => t.page.goBack());
    t.equal(await t.text('main h1'), 'Home', 'back home');
    t.equal(await t.page.evaluate(() => !!document.querySelector('.reports')), false, 'the fragment is gone');
    await t.navigation(() => t.page.goForward());
    t.equal(await t.text('.report h1'), 'Sales', 'forward to the report');
    t.equal(await t.text('button.pin'), 'Pinned 0', 'a new fragment');
    await t.navigation(() => t.page.goForward());
    t.equal(await t.text('.report-list a[href="/reports/2"]'), 'Visits', 'forward to the list');
    await t.follow('nav a[href="/users"]');
    t.equal(await t.text('main ul a[href="/users/1"]'), 'Ada', 'out of the reports');
  }],
];

// runScenarios runs the scenarios against the server at url.
async function runScenarios(browser, url, restart) {
  const results = [];
  for (const [name, run] of scenarios) {
    const page = await browser.newPage();
    const logs = [];
    const responses = [];
    page.on('console', (m) => {
      if (m.type() !== 'error' && m.type() !== 'warning') return;
      if (m.text().startsWith('Failed to load resource') && m.location().url.endsWith('/nope')) return;
      logs.push(`${m.type()}: ${m.text()}`);
    });
    page.on('pageerror', (e) => logs.push(`pageerror: ${e.message}`));
    let navRequests = 0;
    page.on('request', (r) => {
      if (r.headers()['x-vela-navigate']) navRequests++;
    });
    page.on('response', (r) => {
      if (r.request().headers()['x-vela-navigate']) responses.push(r.json().catch(() => null));
    });
    await page.addInitScript(() => {
      document.addEventListener('vela:hydrated', () => { window.__hydrated = true; });
    });
    const t = {
      page,
      async open(path) {
        await page.goto(url + path);
        await page.waitForFunction(() => window.__hydrated === true, null, { timeout: 15000 });
        // Lost on a document load: navigations of the router keep it.
        await page.evaluate(() => { window.__marker = 1; });
      },
      // navigation runs action and waits for the router to finish.
      async navigation(action) {
        await page.evaluate(() => {
          window.__navigated = new Promise((resolve) => document.addEventListener('vela:navigated', () => resolve(true), { once: true }));
        });
        await action();
        await page.waitForFunction(() => window.__navigated, null, { timeout: 15000 });
        await page.evaluate(() => window.__navigated);
        t.equal(await page.evaluate(() => window.__marker), 1, 'no document load');
      },
      requests() {
        return navRequests;
      },
      // formResult runs action and waits for a failed form action.
      async formResult(action) {
        await page.evaluate(() => {
          window.__form = new Promise((resolve) => document.addEventListener('vela:form', () => resolve(true), { once: true }));
        });
        await action();
        await page.waitForFunction(() => window.__form, null, { timeout: 15000 });
        await page.evaluate(() => window.__form);
      },
      restart,
      // signIn posts the sign-in form through the page's request context,
      // which shares its cookies with the page.
      async signIn(name = 'Ada') {
        const response = await page.request.post(url + '/login', { form: { name } });
        if (!response.ok()) throw new Error(`sign-in: ${response.status()}`);
      },
      follow(selector) {
        return t.navigation(() => page.click(selector));
      },
      addLink(href) {
        return page.evaluate((href) => {
          const a = document.createElement('a');
          a.id = 'test-link';
          a.href = href;
          a.textContent = 'test';
          document.body.prepend(a);
        }, href);
      },
      // scripts returns a function that lists the scripts requested since.
      scripts() {
        const list = [];
        page.on('request', (r) => {
          if (r.resourceType() === 'script') list.push(new URL(r.url()).pathname);
        });
        return () => list;
      },
      text(selector) {
        return page.textContent(selector).then((s) => s && s.trim());
      },
      async loads() {
        return (await fetch(url + '/api/loads')).json();
      },
      // last is the JSON of the last navigation response.
      last() {
        return t.lastBody;
      },
      equal(got, want, what) {
        if (got !== want) throw new Error(`${what}: ${JSON.stringify(got)}, want ${JSON.stringify(want)}`);
      },
    };
    const originalNavigation = t.navigation;
    t.navigation = async (action) => {
      await originalNavigation(action);
      t.lastBody = await responses.at(-1);
    };
    let error = '';
    try {
      await run(t);
      if (logs.length) error = `console: ${logs.join('; ')}`;
    } catch (e) {
      error = String(e?.message ?? e) + (logs.length ? ` (console: ${logs.join('; ')})` : '');
    } finally {
      await page.close();
    }
    results.push({ name, error });
  }
  // Without JavaScript every link is a document request.
  const context = await browser.newContext({ javaScriptEnabled: false });
  const page = await context.newPage();
  let error = '';
  try {
    await page.goto(url + '/');
    await Promise.all([page.waitForURL('**/users'), page.click('nav a[href="/users"]')]);
    await Promise.all([page.waitForURL('**/users/2'), page.click('main ul a[href="/users/2"]')]);
    const h1 = (await page.textContent('article h1')).trim();
    if (h1 !== 'Grace') error = `page ${h1}, want Grace`;
  } catch (e) {
    error = String(e?.message ?? e);
  } finally {
    await context.close();
  }
  results.push({ name: 'without JavaScript links load documents', error });

  // Without JavaScript the same form posts documents (post/redirect/get).
  const noScript = await browser.newContext({ javaScriptEnabled: false });
  const formPage = await noScript.newPage();
  error = '';
  try {
    await formPage.goto(url + '/users');
    const [failed] = await Promise.all([formPage.waitForNavigation(), formPage.click('form.add button')]);
    if (failed.status() !== 422 || (await formPage.textContent('form.add .error')).trim() !== 'A name is required.') error = `failure: ${failed.status()}`;
    await formPage.fill('form.add input', 'Ada Two');
    const [added] = await Promise.all([formPage.waitForNavigation(), formPage.click('form.add button')]);
    const names = () => formPage.evaluate(() => [...document.querySelectorAll('.user-list li a')].map((a) => a.textContent).join());
    if (added.status() !== 200 || new URL(formPage.url()).pathname !== '/users' || (await names()) !== 'Ada,Grace,Ada Two') error ||= `add: ${added.status()} ${formPage.url()} ${await names()}`;
    await Promise.all([formPage.waitForNavigation(), formPage.click('button[aria-label="Delete Ada Two"]')]);
    if ((await names()) !== 'Ada,Grace') error ||= `delete: ${await names()}`;
  } catch (e) {
    error = String(e?.message ?? e);
  } finally {
    await noScript.close();
  }
  results.push({ name: 'without JavaScript the form posts documents', error });

  // A deploy: the server restarts with another build while a page is open.
  const deployed = await browser.newPage();
  const logs = [];
  deployed.on('pageerror', (e) => logs.push(e.message));
  error = '';
  try {
    await deployed.addInitScript(() => document.addEventListener('vela:hydrated', () => { window.__hydrated = true; }));
    await deployed.goto(url + '/');
    await deployed.waitForFunction(() => window.__hydrated === true);
    await deployed.evaluate(() => { window.__marker = 1; });
    await restart(['-build', 'next']);
    await Promise.all([deployed.waitForURL('**/users'), deployed.click('nav a[href="/users"]')]);
    await deployed.waitForFunction(() => window.__hydrated === true);
    const state = await deployed.evaluate(() => [window.__marker, document.querySelector('meta[name=vela-build]').content, document.querySelector('main ul a')?.textContent]);
    if (state[0] !== undefined || state[1] !== 'next' || state[2] !== 'Ada') error = `after the deploy: ${JSON.stringify(state)}`;
    if (logs.length) error ||= `errors: ${logs.join('; ')}`;
  } catch (e) {
    error = String(e?.message ?? e);
  } finally {
    await deployed.close();
  }
  results.push({ name: 'a new build id loads the document on the next navigation', error });
  return results;
}

async function main() {
  await build({ root: project, configFile: join(project, 'vite.config.js'), logLevel: 'silent', build: { outDir: join(project, 'dist') } });
  // The source of each built file, to name the preloaded lazy pages.
  const manifest = JSON.parse(readFileSync(join(project, 'dist', '.vite', 'manifest.json'), 'utf8'));
  const files = Object.fromEntries(Object.entries(manifest).map(([key, chunk]) => [chunk.file, key]));
  let server = await startServer();
  const url = server.url;
  // restart starts the server again on the same address, with extra flags.
  const restart = async (extra) => {
    await server.stop();
    server = await startServer(new URL(url).host, extra);
  };
  const browser = await chromium.launch();
  try {
    const pages = [];
    for (const c of cases) pages.push(await check(browser, url, c, files));
    return { pages, navigation: await runScenarios(browser, url, restart) };
  } finally {
    await browser.close();
    await server.stop();
  }
}

main().then(
  (result) => process.stdout.write(JSON.stringify(result)),
  (error) => process.stdout.write(JSON.stringify({ error: String(error?.stack ?? error) })),
);
