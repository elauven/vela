// Экспорт/импорт схемы блоков (JSON) и сборка автономной HTML-страницы.

/* ---------- вспомогательные ---------- */

function esc(s) {
  return String(s == null ? '' : s).replace(/[&<>"']/g, c => (
    { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]
  ));
}
function nl2p(s) {
  return String(s || '').split(/\n+/).map(x => x.trim()).filter(Boolean)
    .map(t => '<p>' + esc(t) + '</p>').join('');
}
function lines(s) {
  return String(s || '').split('\n').map(x => x.trim()).filter(Boolean);
}
function download(name, text, mime) {
  const safe = String(name).replace(/[^\wа-яё\-. ]/gi, '_').trim().replace(/\s+/g, '-') || 'file';
  const blob = new Blob([text], { type: mime + ';charset=utf-8' });
  const a = document.createElement('a');
  a.href = URL.createObjectURL(blob);
  a.download = safe;
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(a.href), 2000);
}

/* ---------- рендер блоков в HTML-строки ---------- */

const RENDER = {
  header(p) {
    return '<header class="v-header' + (p.dark ? ' dark' : '') + '"><div class="wrap v-row">'
      + '<a class="v-logo" href="#">' + esc(p.logo) + '</a>'
      + '<nav class="v-nav">' + (p.menu || []).map(m =>
        '<a href="' + esc(m.href || '#') + '">' + esc(m.label) + '</a>').join('') + '</nav>'
      + '<div class="v-hside">'
      + (p.phone ? '<a class="v-phone" href="tel:' + esc(p.phone) + '">' + esc(p.phone) + '</a>' : '')
      + (p.socials || []).map(s =>
        '<a class="v-soc" href="' + esc(s.href || '#') + '" target="_blank" rel="noopener">' + esc(s.label) + '</a>').join('')
      + (p.cta ? '<a class="v-btn" href="#form">' + esc(p.cta) + '</a>' : '')
      + '</div></div></header>';
  },

  footer(p) {
    return '<footer class="v-footer"><div class="wrap"><div class="v-frow">'
      + '<div><h4>О нас</h4><p>' + esc(p.about) + '</p></div>'
      + '<div><h4>Разделы</h4>' + (p.links || []).map(l =>
        '<a href="' + esc(l.href || '#') + '">' + esc(l.label) + '</a>').join('') + '</div>'
      + '<div><h4>Контакты</h4>'
      + (p.phone ? '<a href="tel:' + esc(p.phone) + '">' + esc(p.phone) + '</a>' : '')
      + (p.email ? '<a href="mailto:' + esc(p.email) + '">' + esc(p.email) + '</a>' : '')
      + '<a href="#">' + esc(p.privacy) + '</a></div>'
      + (p.subscribe ? '<div><h4>' + esc(p.subscribeTitle || 'Подписка') + '</h4>'
        + '<form data-lead><input type="email" required placeholder="Ваш email">'
        + '<button type="submit">Ок</button></form>'
        + '<p class="v-ok" hidden>Спасибо! Вы подписаны.</p></div>' : '')
      + '</div><div class="v-fbot">' + esc(p.copyright) + '</div></div></footer>';
  },

  hero(p) {
    const ov = (+p.overlay || 0) / 100;
    const grad = 'linear-gradient(rgba(13,18,32,' + ov + '),rgba(13,18,32,' + ov + '))';
    const bg = p.bg ? 'background-image:' + grad + ",url('" + esc(p.bg) + "');" : '';
    const al = p.align === 'left' ? ' left' : p.align === 'right' ? ' right' : '';
    return '<section class="v-hero' + al + '" style="min-height:' + (+p.height || 70) + 'vh;' + bg + '"><div class="wrap">'
      + '<h1>' + esc(p.title) + '</h1>'
      + (p.subtitle ? '<p>' + esc(p.subtitle) + '</p>' : '')
      + (p.btn ? '<a class="v-btn big" href="' + esc(p.btnHref || '#form') + '">' + esc(p.btn) + '</a>' : '')
      + '</div></section>';
  },

  text(p) {
    const cols = +p.columns > 1 ? ' style="columns:' + (+p.columns) + ';column-gap:40px"' : '';
    return '<section class="v-text"><div class="wrap narrow">'
      + (p.heading ? '<h2>' + esc(p.heading) + '</h2>' : '')
      + (p.quote ? '<blockquote>' + esc(p.quote) + '</blockquote>' : '')
      + '<div' + cols + '>' + nl2p(p.body) + '</div>'
      + '</div></section>';
  },

  gallery(p) {
    const imgs = (p.images || []).filter(i => i.src);
    let body;
    if (p.mode === 'slider' && imgs.length) {
      body = '<div class="v-gal-slide" data-gal data-images="' + esc(JSON.stringify(imgs)) + '">'
        + '<img src="' + esc(imgs[0].src) + '" alt="' + esc(imgs[0].alt || '') + '">'
        + '<button class="v-garr prev" data-prev type="button" aria-label="Назад">&#8249;</button>'
        + '<button class="v-garr next" data-next type="button" aria-label="Вперёд">&#8250;</button>'
        + '<div class="vdots">' + imgs.map((_, k) =>
          '<button class="gdot' + (k === 0 ? ' active' : '') + '" type="button"></button>').join('') + '</div>'
        + '</div>';
    } else {
      body = '<div class="v-gal-grid" style="grid-template-columns:repeat(' + (+p.cols || 3) + ',1fr)">'
        + imgs.map(i => '<figure><img src="' + esc(i.src) + '" alt="' + esc(i.alt || '') + '" loading="lazy"></figure>').join('')
        + '</div>';
    }
    return '<section class="v-gallery"><div class="wrap">'
      + (p.title ? '<h2>' + esc(p.title) + '</h2>' : '') + body + '</div></section>';
  },

  video(p) {
    let body = '';
    if (p.kind === 'audio') {
      body = '<audio controls src="' + esc(p.url || '') + '"></audio>';
    } else {
      const u = String(p.url || '');
      const yt = u.match(/(?:youtube\.com\/(?:watch\?v=|embed\/)|youtu\.be\/)([\w-]{6,})/);
      const vm = u.match(/vimeo\.com\/(?:video\/)?(\d+)/);
      const src = yt ? 'https://www.youtube.com/embed/' + yt[1]
        : vm ? 'https://player.vimeo.com/video/' + vm[1] : u;
      body = src ? '<div class="v-frame"><iframe src="' + esc(src) + '" title="' + esc(p.title || 'Видео')
        + '" frameborder="0" allowfullscreen></iframe></div>' : '';
    }
    return '<section class="v-video"><div class="wrap">'
      + (p.title ? '<h2>' + esc(p.title) + '</h2>' : '') + body + '</div></section>';
  },

  features(p) {
    return '<section class="v-features"><div class="wrap" id="features">'
      + (p.title ? '<h2>' + esc(p.title) + '</h2>' : '')
      + '<div class="v-fgrid">' + (p.items || []).map(i =>
        '<div class="v-fcard"><div class="v-fico">' + esc(i.icon) + '</div>'
        + '<h3>' + esc(i.title) + '</h3><p>' + esc(i.text) + '</p></div>').join('') + '</div>'
      + '</div></section>';
  },

  pricing(p) {
    return '<section class="v-pricing"><div class="wrap" id="pricing">'
      + (p.title ? '<h2>' + esc(p.title) + '</h2>' : '')
      + '<div class="v-plans">' + (p.plans || []).map(pl =>
        '<div class="v-plan' + (pl.hot ? ' hot' : '') + '">'
        + (pl.hot ? '<div class="v-badge">Популярный</div>' : '')
        + '<h3>' + esc(pl.name) + '</h3><div class="v-price">' + esc(pl.price) + '</div>'
        + '<ul>' + lines(pl.features).map(f => '<li>' + esc(f) + '</li>').join('') + '</ul>'
        + '<a class="v-btn' + (pl.hot ? '' : ' ghost') + '" href="#form">' + esc(pl.cta || 'Выбрать') + '</a>'
        + '</div>').join('') + '</div>'
      + '</div></section>';
  },

  team(p) {
    return '<section class="v-team"><div class="wrap">'
      + (p.title ? '<h2>' + esc(p.title) + '</h2>' : '')
      + '<div class="v-tgrid">' + (p.people || []).map(m =>
        '<div class="v-person">'
        + (m.photo ? '<img src="' + esc(m.photo) + '" alt="' + esc(m.name) + '">'
          : '<div class="v-ph">' + esc((m.name || '?').charAt(0)) + '</div>')
        + '<h3>' + esc(m.name) + '</h3><span>' + esc(m.role) + '</span><p>' + esc(m.bio) + '</p>'
        + '</div>').join('') + '</div>'
      + '</div></section>';
  },

  testimonials(p) {
    return '<section class="v-testi"><div class="wrap" id="testimonials">'
      + (p.title ? '<h2>' + esc(p.title) + '</h2>' : '')
      + '<div class="v-tstgrid">' + (p.items || []).map(t =>
        '<figure class="v-tst"><blockquote>' + esc(t.text) + '</blockquote><figcaption>'
        + (t.photo ? '<img src="' + esc(t.photo) + '" alt="">' : '')
        + '<div><b>' + esc(t.name) + '</b><span>' + esc(t.company) + '</span>'
        + (t.link ? '<a href="' + esc(t.link) + '" target="_blank" rel="noopener">профиль &#8599;</a>' : '')
        + '</div></figcaption></figure>').join('') + '</div>'
      + '</div></section>';
  },

  faq(p) {
    return '<section class="v-faq"><div class="wrap narrow">'
      + (p.title ? '<h2>' + esc(p.title) + '</h2>' : '')
      + (p.items || []).map(it =>
        '<div class="fitem"><button class="fq" type="button">' + esc(it.q)
        + '<span class="pm">+</span></button><div class="fa">' + nl2p(it.a) + '</div></div>').join('')
      + '</div></section>';
  },

  form(p) {
    const f = [];
    if (p.showName !== false) f.push('<input required name="name" placeholder="Имя">');
    if (p.showPhone !== false) f.push('<input required name="phone" type="tel" placeholder="Телефон">');
    if (p.showEmail) f.push('<input name="email" type="email" placeholder="Email">');
    return '<section class="v-form" id="form"><div class="wrap narrow">'
      + '<h2>' + esc(p.title) + '</h2>'
      + (p.subtitle ? '<p class="v-sub">' + esc(p.subtitle) + '</p>' : '')
      + '<form data-lead>' + f.join('')
      + '<button class="v-btn" type="submit">' + esc(p.btn || 'Отправить') + '</button></form>'
      + '<div class="v-ok" hidden>&#10003; Заявка отправлена. Мы свяжемся с вами!</div>'
      + (p.note ? '<p class="v-note">' + esc(p.note) + '</p>' : '')
      + '</div></section>';
  },

  contacts(p) {
    let map = '';
    if (p.map === 'google') {
      map = '<div class="v-map"><iframe loading="lazy" src="'
        + esc('https://maps.google.com/maps?q=' + encodeURIComponent(p.address || '') + '&output=embed')
        + '"></iframe></div>';
    } else if (p.map !== 'none') {
      map = '<div class="v-map"><iframe loading="lazy" src="'
        + esc('https://yandex.ru/map-widget/v1/?text=' + encodeURIComponent(p.address || ''))
        + '"></iframe></div>';
    }
    return '<section class="v-contacts"><div class="wrap" id="contacts">'
      + (p.title ? '<h2>' + esc(p.title) + '</h2>' : '')
      + '<div class="v-cgrid"><div class="v-cinfo">'
      + (p.address ? '<p>&#128205; ' + esc(p.address) + '</p>' : '')
      + (p.phone ? '<p>&#128222; <a href="tel:' + esc(p.phone) + '">' + esc(p.phone) + '</a></p>' : '')
      + (p.email ? '<p>&#9993;&#65039; <a href="mailto:' + esc(p.email) + '">' + esc(p.email) + '</a></p>' : '')
      + (p.messenger ? '<p>&#128172; <a href="' + esc(p.messenger) + '" target="_blank" rel="noopener">Написать в мессенджере</a></p>' : '')
      + '</div>' + map + '</div></div></section>';
  },

  countdown(p) {
    return '<section class="v-count"><div class="wrap narrow">'
      + '<h2>' + esc(p.title) + '</h2>'
      + '<div class="v-timer" data-countdown="' + esc(p.deadline || '') + '">'
      + '<div class="v-tu"><b data-d>0</b><span>дней</span></div>'
      + '<div class="v-tu"><b data-h>00</b><span>часов</span></div>'
      + '<div class="v-tu"><b data-m>00</b><span>минут</span></div>'
      + '<div class="v-tu"><b data-s>00</b><span>секунд</span></div>'
      + '</div>'
      + (p.cta ? '<a class="v-btn big" href="#form">' + esc(p.cta) + '</a>' : '')
      + '</div></section>';
  },

  quiz(p) {
    const steps = p.steps || [];
    return '<section class="v-quiz"><div class="wrap narrow">'
      + (p.title ? '<h2>' + esc(p.title) + '</h2>' : '')
      + '<div class="v-quizbox" data-quiz>'
      + steps.map((s, i) =>
        '<div class="qstep" data-step="' + i + '"' + (i ? ' hidden' : '') + '>'
        + '<h3>' + esc(s.question) + '</h3><div class="qopts">'
        + lines(s.options).map(o =>
          '<button type="button" data-opt="' + esc(o) + '">' + esc(o) + '</button>').join('')
        + '</div></div>').join('')
      + '<div class="qstep qres" data-result hidden><h3>' + esc(p.result || 'Спасибо!') + '</h3>'
      + '<p data-answers></p><a class="v-btn" href="#form">' + esc(p.btn || 'Получить расчёт') + '</a>'
      + '<button type="button" class="v-linkbtn" data-restart>Пройти заново</button></div>'
      + '</div></div></section>';
  },

  calculator(p) {
    return '<section class="v-calc"><div class="wrap narrow">'
      + (p.title ? '<h2>' + esc(p.title) + '</h2>' : '')
      + '<div class="v-calcbox" data-calc data-base="' + esc(p.base) + '" data-unit="' + esc(p.unit || '') + '">'
      + '<label class="v-q">Количество (' + esc(p.unit || 'ед.') + ')'
      + '<input type="range" data-qty min="' + esc(p.min || 1) + '" max="' + esc(p.max || 1000)
      + '" value="' + esc(p.value || 50) + '"><b data-qtyv></b></label>'
      + (p.options || []).map(o =>
        '<label class="v-chk"><input type="checkbox" data-k="' + esc(o.k) + '"> '
        + esc(o.label) + ' <span>&times;' + esc(o.k) + '</span></label>').join('')
      + '<div class="v-total">Итого: <b data-total></b></div>'
      + '</div></div></section>';
  },

  beforeafter(p) {
    const pos = +p.pos || 50;
    return '<section class="v-ba"><div class="wrap">'
      + (p.title ? '<h2>' + esc(p.title) + '</h2>' : '')
      + '<div class="v-babox" data-ba>'
      + '<img src="' + esc(p.before) + '" alt="До">'
      + '<img data-after src="' + esc(p.after) + '" alt="После" style="clip-path:inset(0 0 0 ' + pos + '%)">'
      + '<div class="v-split" data-split style="left:' + pos + '%"></div>'
      + '<span class="v-balab l">До</span><span class="v-balab r">После</span>'
      + '</div>'
      + '<input class="v-barange" type="range" data-pos min="0" max="100" value="' + pos + '">'
      + '</div></section>';
  },

  events(p) {
    return '<section class="v-events"><div class="wrap">'
      + (p.title ? '<h2>' + esc(p.title) + '</h2>' : '')
      + '<div class="v-evlist">' + (p.items || []).map(e =>
        '<div class="v-ev"><div class="v-date"><b>' + esc(e.date) + '</b><span>' + esc(e.time) + '</span></div>'
        + '<div class="v-evinfo"><h3>' + esc(e.title) + '</h3><p>' + esc(e.place) + '</p></div>'
        + '<a class="v-btn ghost" href="' + esc(e.href || '#form') + '">Записаться</a></div>').join('') + '</div>'
      + '</div></section>';
  },

  timeline(p) {
    return '<section class="v-timeline"><div class="wrap">'
      + (p.title ? '<h2>' + esc(p.title) + '</h2>' : '')
      + '<div class="v-steps">' + (p.steps || []).map((s, i) =>
        '<div class="v-step"><div class="v-num">' + (i + 1) + '</div>'
        + '<h3>' + esc(s.title) + '</h3><p>' + esc(s.text) + '</p></div>').join('') + '</div>'
      + '</div></section>';
  },

  stats(p) {
    return '<section class="v-stats"><div class="wrap">'
      + (p.title ? '<h2>' + esc(p.title) + '</h2>' : '')
      + '<div class="v-sgrid">' + (p.items || []).map(i =>
        '<div class="v-stat"><b data-count="' + esc(i.value) + '" data-suffix="' + esc(i.suffix || '') + '">0</b>'
        + '<span>' + esc(i.label) + '</span></div>').join('') + '</div>'
      + '</div></section>';
  }
};

/* ---------- стили экспортируемой страницы ---------- */

const CSS = `
*{box-sizing:border-box}
body{margin:0;font-family:system-ui,-apple-system,'Segoe UI',Roboto,Arial,sans-serif;color:#17203a;line-height:1.55;background:#fff}
img{max-width:100%;display:block}
a{color:#2563eb}
.wrap{max-width:1100px;margin:0 auto;padding:0 20px}
.wrap.narrow{max-width:780px}
section{padding:64px 0}
h1,h2,h3,h4{line-height:1.2;margin:0 0 12px}
section h2{font-size:34px;text-align:center;margin-bottom:36px}
.v-btn{display:inline-block;background:#2563eb;color:#fff;padding:12px 22px;border-radius:10px;text-decoration:none;font-weight:600;border:0;cursor:pointer;font-size:15px}
.v-btn.big{padding:15px 30px;font-size:17px}
.v-btn.ghost{background:transparent;color:#2563eb;border:2px solid #2563eb}
.v-ok{background:#e8f7ee;color:#1d7a3e;border:1px solid #bfe6cd;border-radius:10px;padding:14px 16px;margin-top:14px;font-weight:600}
.v-header{background:#fff;border-bottom:1px solid #e7ebf2}
.v-header.dark{background:#14181f;border-color:#14181f}
.v-header.dark .v-logo,.v-header.dark .v-nav a,.v-header.dark .v-phone{color:#fff}
.v-header.dark .v-soc{color:#cfd6e4;border-color:#39414f}
.v-row{display:flex;align-items:center;gap:24px;flex-wrap:wrap;padding-top:14px;padding-bottom:14px}
.v-logo{font-weight:800;font-size:20px;color:#17203a;text-decoration:none}
.v-nav{display:flex;gap:18px;flex-wrap:wrap}
.v-nav a{color:#3c4a63;text-decoration:none;font-size:14.5px}
.v-hside{margin-left:auto;display:flex;align-items:center;gap:12px;flex-wrap:wrap}
.v-phone{color:#17203a;text-decoration:none;font-weight:600;font-size:14px}
.v-soc{color:#6b7a94;text-decoration:none;font-size:13px;border:1px solid #d7deea;border-radius:8px;padding:4px 9px}
.v-footer{background:#14181f;color:#cfd6e4;padding:48px 0 0}
.v-frow{display:grid;grid-template-columns:repeat(auto-fit,minmax(200px,1fr));gap:28px;padding-bottom:36px}
.v-footer h4{color:#fff;margin-bottom:12px}
.v-footer a{color:#cfd6e4;text-decoration:none;display:block;margin-bottom:8px}
.v-footer a:hover{color:#fff}
.v-fbot{border-top:1px solid #232a38;padding:16px 0;color:#8a94a8;font-size:13px}
.v-footer form{display:flex;gap:8px}
.v-footer input{flex:1;background:#0f1219;border:1px solid #2a3140;border-radius:8px;color:#fff;padding:9px 12px}
.v-footer button{background:#2563eb;border:0;color:#fff;border-radius:8px;padding:9px 14px;cursor:pointer}
.v-hero{display:flex;align-items:center;background-size:cover;background-position:center;background-color:#101623;color:#fff;text-align:center}
.v-hero.left{text-align:left}.v-hero.right{text-align:right}
.v-hero h1{font-size:48px;margin-bottom:14px}
.v-hero p{font-size:19px;opacity:.92;max-width:640px;margin:0 auto 26px}
.v-hero.left p,.v-hero.right p{margin-left:0;margin-right:0}
.v-text blockquote{border-left:4px solid #2563eb;margin:0 0 18px;padding:8px 18px;font-size:19px;font-style:italic;color:#33415e;background:#f3f6fc;border-radius:0 10px 10px 0}
.v-text p{color:#3c4a63;font-size:16.5px}
.v-gal-grid{display:grid;gap:14px}
.v-gal-grid figure{margin:0;border-radius:12px;overflow:hidden}
.v-gal-grid img{width:100%;height:220px;object-fit:cover}
.v-gal-slide{position:relative;border-radius:14px;overflow:hidden}
.v-gal-slide img{width:100%;height:440px;object-fit:cover}
.v-garr{position:absolute;top:50%;transform:translateY(-50%);width:42px;height:42px;border-radius:50%;border:0;background:rgba(20,24,31,.55);color:#fff;font-size:22px;cursor:pointer}
.v-garr.prev{left:14px}.v-garr.next{right:14px}
.vdots{position:absolute;bottom:12px;left:0;right:0;display:flex;gap:6px;justify-content:center}
.gdot{width:9px;height:9px;border-radius:50%;border:0;background:rgba(255,255,255,.5);cursor:pointer;padding:0}
.gdot.active{background:#fff}
.v-frame{position:relative;padding-bottom:56.25%;border-radius:14px;overflow:hidden;background:#000}
.v-frame iframe{position:absolute;inset:0;width:100%;height:100%;border:0}
.v-video audio{width:100%}
.v-fgrid{display:grid;grid-template-columns:repeat(auto-fit,minmax(230px,1fr));gap:18px}
.v-fcard{background:#f5f7fb;border-radius:14px;padding:24px}
.v-fico{font-size:30px;margin-bottom:10px}
.v-fcard h3{font-size:18px}
.v-fcard p{color:#5b6b85;margin:0}
.v-plans{display:grid;grid-template-columns:repeat(auto-fit,minmax(250px,1fr));gap:18px;align-items:start}
.v-plan{border:1px solid #e3e8f2;border-radius:16px;padding:26px;position:relative;background:#fff}
.v-plan.hot{border-color:#2563eb;box-shadow:0 12px 34px rgba(37,99,235,.16)}
.v-badge{position:absolute;top:-12px;left:50%;transform:translateX(-50%);background:#2563eb;color:#fff;font-size:12px;padding:4px 12px;border-radius:999px}
.v-price{font-size:32px;font-weight:800;margin:8px 0 14px}
.v-plan ul{margin:0 0 18px;padding-left:20px;color:#3c4a63}
.v-plan li{margin-bottom:6px}
.v-tgrid{display:grid;grid-template-columns:repeat(auto-fit,minmax(220px,1fr));gap:18px}
.v-person{background:#f5f7fb;border-radius:14px;padding:22px;text-align:center}
.v-person img,.v-ph{width:110px;height:110px;border-radius:50%;object-fit:cover;margin:0 auto 12px}
.v-ph{background:#2563eb;color:#fff;display:flex;align-items:center;justify-content:center;font-size:40px;font-weight:700}
.v-person span{color:#6b7a94;font-size:14px;display:block;margin-bottom:8px}
.v-person p{color:#3c4a63;font-size:14px;margin:0}
.v-tstgrid{display:grid;grid-template-columns:repeat(auto-fit,minmax(260px,1fr));gap:18px}
.v-tst{background:#f5f7fb;border-radius:14px;padding:22px;margin:0}
.v-tst blockquote{margin:0 0 16px;color:#33415e}
.v-tst figcaption{display:flex;gap:10px;align-items:center}
.v-tst img{width:44px;height:44px;border-radius:50%;object-fit:cover}
.v-tst b{display:block}
.v-tst span{color:#6b7a94;font-size:13px;display:block}
.v-tst a{font-size:13px}
.fitem{border:1px solid #e3e8f2;border-radius:12px;margin-bottom:10px;overflow:hidden;background:#fff}
.fq{width:100%;display:flex;justify-content:space-between;gap:12px;background:none;border:0;padding:16px 18px;font-size:16px;font-weight:600;color:#17203a;cursor:pointer;text-align:left}
.fq .pm{color:#2563eb;font-size:20px}
.fa{display:none;padding:0 18px 16px;color:#5b6b85}
.fitem.open .fa{display:block}
.v-form{background:#f5f7fb}
.v-form .v-sub{text-align:center;color:#5b6b85;margin-top:-20px;margin-bottom:26px}
.v-form form{display:grid;gap:12px;max-width:460px;margin:0 auto}
.v-form input{padding:13px 14px;border:1px solid #d7deea;border-radius:10px;font-size:15px}
.v-form .v-note{text-align:center;color:#8a94a8;font-size:12.5px;margin-top:14px}
.v-cgrid{display:grid;grid-template-columns:1fr 1.4fr;gap:26px}
@media(max-width:800px){.v-cgrid{grid-template-columns:1fr}}
.v-cinfo p{margin:0 0 12px;font-size:16px}
.v-map{border-radius:14px;overflow:hidden;min-height:300px}
.v-map iframe{width:100%;height:100%;min-height:300px;border:0}
.v-count{background:#14181f;color:#fff;text-align:center}
.v-count h2{color:#fff}
.v-timer{display:flex;gap:14px;justify-content:center;margin-bottom:28px;flex-wrap:wrap}
.v-tu{background:#1f2634;border-radius:14px;padding:16px 20px;min-width:92px}
.v-tu b{display:block;font-size:34px}
.v-tu span{color:#8a94a8;font-size:13px}
.v-quizbox{background:#f5f7fb;border-radius:16px;padding:26px}
.v-quizbox h3{font-size:20px}
.qopts{display:grid;gap:10px}
.qopts button{background:#fff;border:1.5px solid #d7deea;border-radius:10px;padding:12px 14px;font-size:15px;cursor:pointer;text-align:left}
.qopts button:hover{border-color:#2563eb}
.v-linkbtn{background:none;border:0;color:#2563eb;cursor:pointer;margin-left:12px}
.v-calcbox{background:#f5f7fb;border-radius:16px;padding:26px}
.v-q{display:grid;gap:8px;margin-bottom:16px;font-weight:600}
.v-q input[type=range]{width:100%}
.v-chk{display:flex;gap:8px;align-items:center;margin-bottom:8px}
.v-chk span{color:#8a94a8}
.v-total{margin-top:16px;font-size:20px}
.v-babox{position:relative;border-radius:14px;overflow:hidden;max-width:820px;margin:0 auto}
.v-babox img{width:100%;height:460px;object-fit:cover}
.v-babox img[data-after]{position:absolute;inset:0}
.v-split{position:absolute;top:0;bottom:0;width:3px;background:#fff;transform:translateX(-50%)}
.v-balab{position:absolute;top:12px;background:rgba(20,24,31,.6);color:#fff;padding:4px 10px;border-radius:8px;font-size:13px}
.v-balab.l{left:12px}.v-balab.r{right:12px}
.v-barange{display:block;width:100%;max-width:820px;margin:14px auto 0}
.v-evlist{display:grid;gap:12px;max-width:860px;margin:0 auto}
.v-ev{display:flex;gap:18px;align-items:center;border:1px solid #e3e8f2;border-radius:14px;padding:16px 18px;flex-wrap:wrap}
.v-date{background:#f5f7fb;border-radius:10px;padding:10px 14px;text-align:center;min-width:96px}
.v-date b{display:block}
.v-date span{color:#6b7a94;font-size:13px}
.v-evinfo{flex:1}
.v-evinfo h3{margin-bottom:4px}
.v-evinfo p{margin:0;color:#6b7a94}
.v-steps{display:grid;grid-template-columns:repeat(auto-fit,minmax(200px,1fr));gap:18px}
.v-step{background:#f5f7fb;border-radius:14px;padding:22px}
.v-num{width:38px;height:38px;border-radius:50%;background:#2563eb;color:#fff;display:flex;align-items:center;justify-content:center;font-weight:700;margin-bottom:12px}
.v-step p{color:#5b6b85;margin:0}
.v-stats{background:#2563eb;color:#fff}
.v-stats h2{color:#fff}
.v-sgrid{display:grid;grid-template-columns:repeat(auto-fit,minmax(180px,1fr));gap:20px;text-align:center}
.v-stat b{font-size:44px;display:block}
.v-stat span{opacity:.85}
@media(max-width:640px){.v-hero h1{font-size:34px}section h2{font-size:27px}}
`;

/* ---------- маленький рантайм для интерактивных блоков в экспорте ----------
   Классический JS без шаблонных строк, чтобы ничего не конфликтовало.       */

const RUNTIME = `<script>
(function () {
  function $(s, r) { return (r || document).querySelector(s); }
  function $all(s, r) { return Array.prototype.slice.call((r || document).querySelectorAll(s)); }

  /* FAQ-аккордеон */
  $all('.v-faq .fitem').forEach(function (item) {
    $('.fq', item).addEventListener('click', function () {
      var wasOpen = item.classList.contains('open');
      $all('.v-faq .fitem').forEach(function (x) { x.classList.remove('open'); });
      if (!wasOpen) item.classList.add('open');
    });
  });

  /* Таймер обратного отсчёта */
  $all('[data-countdown]').forEach(function (box) {
    var end = new Date(box.getAttribute('data-countdown')).getTime() || 0;
    var d = $('[data-d]', box), h = $('[data-h]', box), m = $('[data-m]', box), s = $('[data-s]', box);
    function pad(n) { return (n < 10 ? '0' : '') + n; }
    function tick() {
      var t = Math.max(0, end - Date.now());
      d.textContent = Math.floor(t / 86400000);
      h.textContent = pad(Math.floor(t / 3600000) % 24);
      m.textContent = pad(Math.floor(t / 60000) % 60);
      s.textContent = pad(Math.floor(t / 1000) % 60);
    }
    tick(); setInterval(tick, 1000);
  });

  /* Квиз */
  $all('[data-quiz]').forEach(function (quiz) {
    var step = 0, answers = [];
    var steps = $all('.qstep[data-step]', quiz);
    var res = $('[data-result]', quiz);
    function show(i) {
      steps.forEach(function (s, k) { s.hidden = k !== i; });
      if (res) res.hidden = true;
    }
    $all('[data-opt]', quiz).forEach(function (btn) {
      btn.addEventListener('click', function () {
        answers[step] = btn.getAttribute('data-opt');
        if (step + 1 < steps.length) { step = step + 1; show(step); }
        else {
          steps.forEach(function (s) { s.hidden = true; });
          if (res) {
            res.hidden = false;
            var out = $('[data-answers]', res);
            if (out) out.textContent = answers.join(' \\u2192 ');
          }
        }
      });
    });
    var rbtn = $('[data-restart]', quiz);
    if (rbtn) rbtn.addEventListener('click', function () { answers = []; step = 0; show(0); });
  });

  /* Калькулятор */
  $all('[data-calc]').forEach(function (box) {
    var base = parseFloat(box.getAttribute('data-base')) || 0;
    var unit = box.getAttribute('data-unit') || '';
    var qty = $('[data-qty]', box), qtyv = $('[data-qtyv]', box), out = $('[data-total]', box);
    function calc() {
      var q = parseFloat(qty.value) || 0, k = 1;
      $all('input[data-k]', box).forEach(function (c) {
        if (c.checked) k = k * (parseFloat(c.getAttribute('data-k')) || 1);
      });
      qtyv.textContent = q + ' ' + unit;
      out.textContent = Math.round(base * q * k).toLocaleString('ru-RU') + ' \\u20bd';
    }
    qty.addEventListener('input', calc);
    $all('input[data-k]', box).forEach(function (c) { c.addEventListener('change', calc); });
    calc();
  });

  /* До/После */
  $all('[data-ba]').forEach(function (box) {
    var img = $('[data-after]', box), line = $('[data-split]', box), r = $('[data-pos]', box);
    function apply() {
      img.style.clipPath = 'inset(0 0 0 ' + r.value + '%)';
      line.style.left = r.value + '%';
    }
    r.addEventListener('input', apply); apply();
  });

  /* Слайдер-галерея */
  $all('[data-gal]').forEach(function (box) {
    var imgs = [];
    try { imgs = JSON.parse(box.getAttribute('data-images') || '[]'); } catch (e) {}
    if (!imgs.length) return;
    var i = 0, el = $('img', box);
    function show(n) {
      i = (n + imgs.length) % imgs.length;
      el.src = imgs[i].src; el.alt = imgs[i].alt || '';
      $all('.gdot', box).forEach(function (d, k) { d.classList.toggle('active', k === i); });
    }
    var pr = $('[data-prev]', box), nx = $('[data-next]', box);
    if (pr) pr.addEventListener('click', function () { show(i - 1); });
    if (nx) nx.addEventListener('click', function () { show(i + 1); });
    $all('.gdot', box).forEach(function (d, k) { d.addEventListener('click', function () { show(k); }); });
  });

  /* Анимированные счётчики */
  $all('[data-count]').forEach(function (el) {
    var target = parseFloat(el.getAttribute('data-count')) || 0;
    var suf = el.getAttribute('data-suffix') || '';
    var t0 = null;
    function frame(t) {
      if (t0 === null) t0 = t;
      var p = Math.min(1, (t - t0) / 1200);
      el.textContent = Math.round(target * p) + suf;
      if (p < 1) requestAnimationFrame(frame);
    }
    requestAnimationFrame(frame);
  });

  /* Формы: имитация отправки */
  $all('form[data-lead]').forEach(function (form) {
    form.addEventListener('submit', function (e) {
      e.preventDefault();
      var ok = form.nextElementSibling;
      if (ok && ok.classList.contains('v-ok')) { form.style.display = 'none'; ok.hidden = false; }
      else form.reset();
    });
  });
})();
<\/script>`;

/* ---------- публичный API ---------- */

export function buildHtml(page) {
  const body = (page.blocks || [])
    .map(b => (RENDER[b.type] ? '<!-- block:' + b.type + ' -->\n' + RENDER[b.type](b.props || {}) : ''))
    .join('\n');
  return '<!doctype html>\n<html lang="ru">\n<head>\n'
    + '<meta charset="utf-8">\n<meta name="viewport" content="width=device-width, initial-scale=1">\n'
    + '<title>' + esc(page.title || 'Страница') + '</title>\n'
    + '<style>' + CSS + '</style>\n'
    + '</head>\n<body>\n' + body + '\n' + RUNTIME + '\n</body>\n</html>\n';
}

export function exportHtmlFile(page) {
  download((page.title || 'page') + '.html', buildHtml(page), 'text/html');
}

export function exportJsonFile(page) {
  const schema = { version: 1, title: page.title, blocks: page.blocks };
  download((page.title || 'page') + '.vela.json', JSON.stringify(schema, null, 2), 'application/json');
}

// Импорт схемы: валидируем и нормализуем. Неизвестные типы блоков отбрасываются.
export function parseSchema(text) {
  try {
    const data = JSON.parse(text);
    if (!data || !Array.isArray(data.blocks)) return null;
    const blocks = data.blocks
      .filter(b => b && typeof b.type === 'string' && RENDER[b.type])
      .map((b, i) => ({
        id: b.id || 'imp' + i + '-' + Math.random().toString(36).slice(2, 7),
        type: b.type,
        props: b.props || JSON.parse(JSON.stringify({}))
      }));
    return { title: data.title || 'Импортированная страница', blocks };
  } catch {
    return null;
  }
}
