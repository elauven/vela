// Реестр блоков: группы, иконки и настройки полей инспектора.
// Виды полей: text | textarea | number | check | datetime | seg | list | strings
//   seg     — переключатель из вариантов [{v, t}]
//   list    — список объектов; поля элемента описываются в fields
//   strings — список строк

export const GROUPS = [
  'Шапка и подвал',
  'Первый экран и контент',
  'Продажи и услуги',
  'Конверсия',
  'Интерактив и вовлечение',
  'Этапы и процессы'
];

const linkList = [
  { name: 'label', label: 'Текст' },
  { name: 'href', label: 'Ссылка' }
];

export const BLOCKS = [
  {
    type: 'header', group: 'Шапка и подвал', label: 'Шапка', icon: '🧭', fields: [
      { name: 'logo', label: 'Логотип (текст)', kind: 'text' },
      { name: 'menu', label: 'Пункты меню', kind: 'list', fields: linkList },
      { name: 'phone', label: 'Телефон', kind: 'text' },
      { name: 'cta', label: 'Кнопка CTA (текст)', kind: 'text' },
      { name: 'socials', label: 'Соцсети', kind: 'list', fields: linkList },
      { name: 'dark', label: 'Тёмная шапка', kind: 'check' }
    ]
  },
  {
    type: 'footer', group: 'Шапка и подвал', label: 'Подвал', icon: '📄', fields: [
      { name: 'about', label: 'О компании', kind: 'textarea', rows: 3 },
      { name: 'links', label: 'Ссылки разделов', kind: 'list', fields: linkList },
      { name: 'copyright', label: 'Копирайт', kind: 'text' },
      { name: 'privacy', label: 'Текст политики конфиденциальности', kind: 'text' },
      { name: 'phone', label: 'Телефон', kind: 'text' },
      { name: 'email', label: 'Email', kind: 'text' },
      { name: 'subscribe', label: 'Форма подписки', kind: 'check' },
      { name: 'subscribeTitle', label: 'Заголовок формы подписки', kind: 'text' }
    ]
  },

  {
    type: 'hero', group: 'Первый экран и контент', label: 'Первый экран (обложка)', icon: '🌄', fields: [
      { name: 'title', label: 'Заголовок', kind: 'text' },
      { name: 'subtitle', label: 'Подзаголовок', kind: 'textarea', rows: 2 },
      { name: 'btn', label: 'Текст главной кнопки', kind: 'text' },
      { name: 'btnHref', label: 'Ссылка кнопки', kind: 'text' },
      { name: 'bg', label: 'Фоновое изображение (URL)', kind: 'text' },
      { name: 'overlay', label: 'Затемнение фона, %', kind: 'number' },
      { name: 'height', label: 'Высота, vh', kind: 'number' },
      {
        name: 'align', label: 'Выравнивание', kind: 'seg', options: [
          { v: 'left', t: 'Слева' }, { v: 'center', t: 'Центр' }, { v: 'right', t: 'Справа' }
        ]
      }
    ]
  },
  {
    type: 'text', group: 'Первый экран и контент', label: 'Текстовый блок', icon: '📝', fields: [
      { name: 'heading', label: 'Заголовок', kind: 'text' },
      { name: 'body', label: 'Текст (абзацы через пустую строку)', kind: 'textarea', rows: 6 },
      { name: 'columns', label: 'Колонок текста', kind: 'number' },
      { name: 'quote', label: 'Цитата (необязательно)', kind: 'text' }
    ]
  },
  {
    type: 'gallery', group: 'Первый экран и контент', label: 'Галерея / слайдер', icon: '🖼️', fields: [
      { name: 'title', label: 'Заголовок', kind: 'text' },
      {
        name: 'mode', label: 'Вид', kind: 'seg', options: [
          { v: 'grid', t: 'Сетка' }, { v: 'slider', t: 'Слайдер' }
        ]
      },
      { name: 'cols', label: 'Колонок в сетке', kind: 'number' },
      {
        name: 'images', label: 'Изображения', kind: 'list', fields: [
          { name: 'src', label: 'URL картинки' },
          { name: 'alt', label: 'Подпись (alt)' }
        ]
      }
    ]
  },
  {
    type: 'video', group: 'Первый экран и контент', label: 'Видео / аудио', icon: '▶️', fields: [
      { name: 'title', label: 'Заголовок', kind: 'text' },
      {
        name: 'kind', label: 'Тип', kind: 'seg', options: [
          { v: 'video', t: 'Видео' }, { v: 'audio', t: 'Аудио' }
        ]
      },
      { name: 'url', label: 'Ссылка (YouTube, Vimeo или файл)', kind: 'text' }
    ]
  },

  {
    type: 'features', group: 'Продажи и услуги', label: 'Услуги / преимущества', icon: '⚡', fields: [
      { name: 'title', label: 'Заголовок', kind: 'text' },
      {
        name: 'items', label: 'Карточки', kind: 'list', fields: [
          { name: 'icon', label: 'Иконка (эмодзи)' },
          { name: 'title', label: 'Название' },
          { name: 'text', label: 'Описание', kind: 'textarea' }
        ]
      }
    ]
  },
  {
    type: 'pricing', group: 'Продажи и услуги', label: 'Тарифы', icon: '💳', fields: [
      { name: 'title', label: 'Заголовок', kind: 'text' },
      {
        name: 'plans', label: 'Тарифы', kind: 'list', fields: [
          { name: 'name', label: 'Название' },
          { name: 'price', label: 'Цена' },
          { name: 'features', label: 'Возможности (по строке)', kind: 'textarea' },
          { name: 'cta', label: 'Текст кнопки' },
          { name: 'hot', label: 'Популярный (1/0)', kind: 'number' }
        ]
      }
    ]
  },
  {
    type: 'team', group: 'Продажи и услуги', label: 'Команда', icon: '👥', fields: [
      { name: 'title', label: 'Заголовок', kind: 'text' },
      {
        name: 'people', label: 'Сотрудники', kind: 'list', fields: [
          { name: 'photo', label: 'Фото (URL)' },
          { name: 'name', label: 'Имя' },
          { name: 'role', label: 'Должность' },
          { name: 'bio', label: 'Описание', kind: 'textarea' }
        ]
      }
    ]
  },
  {
    type: 'testimonials', group: 'Продажи и услуги', label: 'Отзывы', icon: '💬', fields: [
      { name: 'title', label: 'Заголовок', kind: 'text' },
      {
        name: 'items', label: 'Отзывы', kind: 'list', fields: [
          { name: 'text', label: 'Текст отзыва', kind: 'textarea' },
          { name: 'name', label: 'Имя' },
          { name: 'company', label: 'Компания' },
          { name: 'photo', label: 'Фото (URL)' },
          { name: 'link', label: 'Ссылка на соцсеть' }
        ]
      }
    ]
  },
  {
    type: 'faq', group: 'Продажи и услуги', label: 'FAQ (аккордеон)', icon: '❓', fields: [
      { name: 'title', label: 'Заголовок', kind: 'text' },
      {
        name: 'items', label: 'Вопросы', kind: 'list', fields: [
          { name: 'q', label: 'Вопрос' },
          { name: 'a', label: 'Ответ', kind: 'textarea' }
        ]
      }
    ]
  },

  {
    type: 'form', group: 'Конверсия', label: 'Форма захвата', icon: '📨', fields: [
      { name: 'title', label: 'Заголовок', kind: 'text' },
      { name: 'subtitle', label: 'Подзаголовок', kind: 'text' },
      { name: 'showName', label: 'Поле «Имя»', kind: 'check' },
      { name: 'showPhone', label: 'Поле «Телефон»', kind: 'check' },
      { name: 'showEmail', label: 'Поле «Email»', kind: 'check' },
      { name: 'btn', label: 'Текст кнопки', kind: 'text' },
      { name: 'note', label: 'Примечание под формой', kind: 'text' }
    ]
  },
  {
    type: 'contacts', group: 'Конверсия', label: 'Контакты и карта', icon: '📍', fields: [
      { name: 'title', label: 'Заголовок', kind: 'text' },
      { name: 'address', label: 'Адрес', kind: 'text' },
      { name: 'phone', label: 'Телефон', kind: 'text' },
      { name: 'email', label: 'Email', kind: 'text' },
      { name: 'messenger', label: 'Мессенджер (ссылка)', kind: 'text' },
      {
        name: 'map', label: 'Карта', kind: 'seg', options: [
          { v: 'yandex', t: 'Яндекс' }, { v: 'google', t: 'Google' }, { v: 'none', t: 'Без карты' }
        ]
      }
    ]
  },
  {
    type: 'countdown', group: 'Конверсия', label: 'Таймер обратного отсчёта', icon: '⏳', fields: [
      { name: 'title', label: 'Заголовок', kind: 'text' },
      { name: 'deadline', label: 'Дата окончания акции', kind: 'datetime' },
      { name: 'cta', label: 'Текст кнопки', kind: 'text' }
    ]
  },

  {
    type: 'quiz', group: 'Интерактив и вовлечение', label: 'Квиз / опрос', icon: '🧠', fields: [
      { name: 'title', label: 'Заголовок', kind: 'text' },
      {
        name: 'steps', label: 'Шаги', kind: 'list', fields: [
          { name: 'question', label: 'Вопрос' },
          { name: 'options', label: 'Варианты (по строке)', kind: 'textarea' }
        ]
      },
      { name: 'result', label: 'Текст результата', kind: 'textarea', rows: 2 },
      { name: 'btn', label: 'Кнопка на результате', kind: 'text' }
    ]
  },
  {
    type: 'calculator', group: 'Интерактив и вовлечение', label: 'Калькулятор стоимости', icon: '🧮', fields: [
      { name: 'title', label: 'Заголовок', kind: 'text' },
      { name: 'unit', label: 'Единица измерения', kind: 'text' },
      { name: 'base', label: 'Цена за единицу, ₽', kind: 'number' },
      { name: 'min', label: 'Минимум', kind: 'number' },
      { name: 'max', label: 'Максимум', kind: 'number' },
      { name: 'value', label: 'Значение по умолчанию', kind: 'number' },
      {
        name: 'options', label: 'Доп. опции (множители)', kind: 'list', fields: [
          { name: 'label', label: 'Название' },
          { name: 'k', label: 'Множитель', kind: 'number' }
        ]
      }
    ]
  },
  {
    type: 'beforeafter', group: 'Интерактив и вовлечение', label: 'До / После (слайдер)', icon: '↔️', fields: [
      { name: 'title', label: 'Заголовок', kind: 'text' },
      { name: 'before', label: 'Картинка «До» (URL)', kind: 'text' },
      { name: 'after', label: 'Картинка «После» (URL)', kind: 'text' },
      { name: 'pos', label: 'Начальная позиция, %', kind: 'number' }
    ]
  },
  {
    type: 'events', group: 'Интерактив и вовлечение', label: 'События и афиша', icon: '📅', fields: [
      { name: 'title', label: 'Заголовок', kind: 'text' },
      {
        name: 'items', label: 'События', kind: 'list', fields: [
          { name: 'date', label: 'Дата (ГГГГ-ММ-ДД)' },
          { name: 'time', label: 'Время' },
          { name: 'title', label: 'Название' },
          { name: 'place', label: 'Место' },
          { name: 'href', label: 'Ссылка записи' }
        ]
      }
    ]
  },

  {
    type: 'timeline', group: 'Этапы и процессы', label: 'Таймлайн / схема работы', icon: '🪜', fields: [
      { name: 'title', label: 'Заголовок', kind: 'text' },
      {
        name: 'steps', label: 'Шаги', kind: 'list', fields: [
          { name: 'title', label: 'Название шага' },
          { name: 'text', label: 'Описание', kind: 'textarea' }
        ]
      }
    ]
  },
  {
    type: 'stats', group: 'Этапы и процессы', label: 'Инфографика / цифры', icon: '📈', fields: [
      { name: 'title', label: 'Заголовок', kind: 'text' },
      {
        name: 'items', label: 'Показатели', kind: 'list', fields: [
          { name: 'value', label: 'Число', kind: 'number' },
          { name: 'suffix', label: 'Суффикс (лет, +, %)' },
          { name: 'label', label: 'Подпись' }
        ]
      }
    ]
  }
];

export function blockDef(type) {
  return BLOCKS.find(b => b.type === type) || null;
}
