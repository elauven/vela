// Дефолтное содержимое каждого типа блока + стартовый шаблон страницы.

export const DEFAULTS = {
  header: {
    logo: 'ВашБренд',
    menu: [
      { label: 'Главная', href: '#' },
      { label: 'Преимущества', href: '#features' },
      { label: 'Тарифы', href: '#pricing' },
      { label: 'Отзывы', href: '#testimonials' },
      { label: 'Контакты', href: '#contacts' }
    ],
    phone: '+7 900 000-00-00',
    cta: 'Оставить заявку',
    socials: [
      { label: 'VK', href: 'https://vk.com' },
      { label: 'TG', href: 'https://t.me' }
    ],
    dark: false
  },

  footer: {
    about: 'Делаем современные сайты и сервисы для бизнеса. Быстро, аккуратно, с гарантией результата.',
    links: [
      { label: 'Преимущества', href: '#features' },
      { label: 'Тарифы', href: '#pricing' },
      { label: 'Отзывы', href: '#testimonials' },
      { label: 'Контакты', href: '#contacts' }
    ],
    copyright: '© 2026 ВашБренд. Все права защищены.',
    privacy: 'Политика конфиденциальности',
    email: 'hello@yourbrand.ru',
    phone: '+7 900 000-00-00',
    subscribe: true,
    subscribeTitle: 'Подпишитесь на новости'
  },

  hero: {
    title: 'Создаём сайты, которые продают',
    subtitle: 'Запустим современный лендинг для вашего бизнеса за 7 дней — под ключ, с текстами и аналитикой.',
    btn: 'Оставить заявку',
    btnHref: '#form',
    bg: 'https://picsum.photos/seed/vela-hero/1600/900',
    overlay: 45,
    align: 'center',
    height: 80
  },

  text: {
    heading: 'О компании',
    body: 'Мы работаем с 2016 года и помогли сотням компаний выйти в онлайн.\nНаша команда — дизайнеры, разработчики и маркетологи, которые отвечают за результат, а не за «красивые картинки».',
    columns: 1,
    quote: 'Хороший сайт — это продавец, который работает круглосуточно.'
  },

  gallery: {
    title: 'Наши работы',
    images: [
      { src: 'https://picsum.photos/seed/vela-g1/800/600', alt: 'Проект 1' },
      { src: 'https://picsum.photos/seed/vela-g2/800/600', alt: 'Проект 2' },
      { src: 'https://picsum.photos/seed/vela-g3/800/600', alt: 'Проект 3' },
      { src: 'https://picsum.photos/seed/vela-g4/800/600', alt: 'Проект 4' },
      { src: 'https://picsum.photos/seed/vela-g5/800/600', alt: 'Проект 5' },
      { src: 'https://picsum.photos/seed/vela-g6/800/600', alt: 'Проект 6' }
    ],
    cols: 3,
    mode: 'grid'
  },

  video: {
    title: 'Видео о нас',
    url: 'https://www.youtube.com/watch?v=dQw4w9WgXcQ',
    kind: 'video'
  },

  features: {
    title: 'Почему выбирают нас',
    items: [
      { icon: '⚡', title: 'Быстрый старт', text: 'Запуск проекта за 7 дней: прототип, дизайн и вёрстка без бюрократии.' },
      { icon: '🎯', title: 'Продающий контент', text: 'Пишем тексты и собираем структуру под вашу аудиторию и её боли.' },
      { icon: '🛡️', title: 'Гарантия результата', text: 'Фиксируем сроки и бюджет в договоре. Поддержка после запуска.' },
      { icon: '📈', title: 'Аналитика с первого дня', text: 'Подключаем метрики и цели, чтобы вы видели заявки и конверсию.' }
    ]
  },

  pricing: {
    title: 'Тарифы',
    plans: [
      { name: 'Старт', price: '29 900 ₽', features: 'Одностраничный лендинг\nАдаптивная вёрстка\nФорма заявки', cta: 'Выбрать', hot: false },
      { name: 'Бизнес', price: '59 900 ₽', features: 'До 5 страниц\nБазовое SEO\nАналитика и цели\nПанель управления', cta: 'Выбрать', hot: true },
      { name: 'Премиум', price: '129 900 ₽', features: 'До 15 страниц\nУникальный дизайн\nИнтеграция с CRM\nГод поддержки', cta: 'Выбрать', hot: false }
    ]
  },

  team: {
    title: 'Команда',
    people: [
      { photo: 'https://i.pravatar.cc/300?img=32', name: 'Анна Соколова', role: 'Арт-директор', bio: '10 лет в дизайне, любит строгие сетки и смелые акценты.' },
      { photo: 'https://i.pravatar.cc/300?img=12', name: 'Дмитрий Орлов', role: 'Ведущий разработчик', bio: 'Отвечает за скорость и надёжность каждого проекта.' },
      { photo: 'https://i.pravatar.cc/300?img=47', name: 'Мария Ким', role: 'Маркетолог', bio: 'Настраивает аналитику и делает так, чтобы сайт продавал.' }
    ]
  },

  testimonials: {
    title: 'Отзывы клиентов',
    items: [
      { text: 'Сделали лендинг за неделю, заявки пошли в первый же день. Очень довольны скоростью и качеством.', name: 'Иван Петров', company: 'ООО «СтройГрад»', photo: 'https://i.pravatar.cc/100?img=13', link: 'https://vk.com' },
      { text: 'Впервые встречаю подрядчика, который сам предлагает улучшения. Конверсия выросла в два раза.', name: 'Ольга Смирнова', company: 'Студия «Уют»', photo: 'https://i.pravatar.cc/100?img=44', link: 'https://t.me' },
      { text: 'Понятный процесс, прозрачные сроки, отличный результат. Рекомендую всем коллегам.', name: 'Сергей Волков', company: 'Volkov Legal', photo: 'https://i.pravatar.cc/100?img=59', link: '' }
    ]
  },

  faq: {
    title: 'Частые вопросы',
    items: [
      { q: 'Сколько времени занимает разработка?', a: 'Лендинг — от 7 дней, многостраничный сайт — от 3 недель. Точный срок фиксируем в договоре.' },
      { q: 'Что нужно от меня для старта?', a: 'Заполненный бриф, логотип и материалы (фото, тексты — если есть). Поможем собрать всё вместе.' },
      { q: 'Можно ли вносить правки после запуска?', a: 'Да. В первый месяц мелкие правки бесплатны, дальше — по тарифам поддержки.' },
      { q: 'Вы помогаете с наполнением и текстами?', a: 'Да, у нас есть редактор: напишем тексты под вашу аудиторию и подберём изображения.' }
    ]
  },

  form: {
    title: 'Оставьте заявку',
    subtitle: 'Ответим в течение рабочего дня и подготовим расчёт.',
    showName: true,
    showPhone: true,
    showEmail: true,
    btn: 'Отправить заявку',
    note: 'Нажимая кнопку, вы соглашаетесь с политикой конфиденциальности.'
  },

  contacts: {
    title: 'Контакты',
    address: 'Москва, ул. Тверская, 1',
    phone: '+7 900 000-00-00',
    email: 'hello@yourbrand.ru',
    messenger: 'https://t.me/yourbrand',
    map: 'yandex'
  },

  countdown: {
    title: 'До конца акции −30% осталось',
    deadline: '2026-12-31T23:59',
    cta: 'Успеть со скидкой'
  },

  quiz: {
    title: 'Ответьте на 3 вопроса и получите расчёт',
    steps: [
      { question: 'Какой сайт вам нужен?', options: 'Лендинг\nКорпоративный сайт\nИнтернет-магазин' },
      { question: 'Когда планируете запуск?', options: 'Срочно, до недели\nВ течение месяца\nПока изучаю' },
      { question: 'Какой бюджет рассматриваете?', options: 'До 50 000 ₽\n50–150 тыс. ₽\nБольше 150 тыс. ₽' }
    ],
    result: 'Спасибо! Предварительный расчёт готов — оставьте контакты, и мы пришлём детали.',
    btn: 'Получить расчёт'
  },

  calculator: {
    title: 'Калькулятор стоимости уборки',
    unit: 'м²',
    base: 90,
    min: 20,
    max: 500,
    value: 60,
    options: [
      { label: 'Генеральная уборка', k: 1.5 },
      { label: 'Мытьё окон', k: 1.2 }
    ]
  },

  beforeafter: {
    title: 'До и после ретуши',
    before: 'https://picsum.photos/seed/vela-before/1200/700',
    after: 'https://picsum.photos/seed/vela-after/1200/700',
    pos: 50
  },

  events: {
    title: 'Ближайшие события',
    items: [
      { date: '2026-09-10', time: '19:00', title: 'Вебинар: сайт, который продаёт', place: 'Онлайн, бесплатно', href: '#form' },
      { date: '2026-09-24', time: '11:00', title: 'Курс: основы веб-аналитики', place: 'Онлайн, 2 дня', href: '#form' },
      { date: '2026-10-05', time: '18:30', title: 'Митап: дизайн и конверсия', place: 'Москва, лофт «Цифра»', href: '#form' }
    ]
  },

  timeline: {
    title: 'Как мы работаем',
    steps: [
      { title: 'Заявка', text: 'Вы оставляете заявку, мы задаём уточняющие вопросы.' },
      { title: 'Бриф и смета', text: 'Составляем бриф, фиксируем объём, сроки и стоимость.' },
      { title: 'Дизайн и вёрстка', text: 'Показываем макеты, вносим правки, верстаем и программируем.' },
      { title: 'Запуск', text: 'Публикуем сайт, подключаем аналитику и передаём доступы.' }
    ]
  },

  stats: {
    title: 'Нам доверяют',
    items: [
      { value: 10, suffix: ' лет', label: 'на рынке' },
      { value: 520, suffix: '+', label: 'проектов запущено' },
      { value: 98, suffix: '%', label: 'клиентов рекомендуют нас' },
      { value: 24, suffix: '/7', label: 'поддержка' }
    ]
  }
};

// Стартовый шаблон — готовый демо-лендинг.
export function makeStarterPage() {
  let n = 0;
  const uid = () => 'b' + (++n) + '-' + Math.random().toString(36).slice(2, 8);
  const pick = (type) => ({ id: uid(), type, props: JSON.parse(JSON.stringify(DEFAULTS[type])) });
  return {
    title: 'Демо-лендинг',
    blocks: [
      'header', 'hero', 'features', 'stats', 'pricing', 'gallery',
      'team', 'testimonials', 'faq', 'countdown', 'quiz', 'form',
      'contacts', 'footer'
    ].map(pick)
  };
}
