-- 0010_seed.sql
-- Стартовые данные: Россия, Ставропольский край, Ставрополь и Михайловск,
-- зарезервированные ники, фича-флаги, достижения.
--
-- ВАЖНО про возрастные пороги. Для России: паспорт выдаётся в 14 лет,
-- совершеннолетие наступает в 18 (ГК РФ, ст. 21). Поэтому min_signup_age = 14,
-- age_of_majority = 18.
-- Для ЛЮБОЙ другой страны эти два числа нужно уточнить у юриста до запуска:
-- в ЕС возраст цифрового согласия по GDPR (ст. 8) варьируется от 13 до 16.
-- Ни одной страны кроме РФ здесь намеренно нет — чтобы никто не скопировал
-- непроверенные цифры.

-- +goose Up

INSERT INTO countries (code, name, currency, default_locale, phone_prefix,
                       week_starts_on, min_signup_age, age_of_majority, is_enabled)
VALUES ('RU', 'Россия', 'RUB', 'ru', '+7', 1, 14, 18, true)
ON CONFLICT (code) DO NOTHING;

INSERT INTO regions (country_id, name)
SELECT id, 'Ставропольский край' FROM countries WHERE code = 'RU'
ON CONFLICT DO NOTHING;

INSERT INTO cities (country_id, region_id, name, slug, timezone, status)
SELECT c.id, r.id, 'Ставрополь', 'stavropol', 'Europe/Moscow', 'pilot'
  FROM countries c
  JOIN regions r ON r.country_id = c.id AND r.name = 'Ставропольский край'
 WHERE c.code = 'RU'
ON CONFLICT DO NOTHING;

INSERT INTO cities (country_id, region_id, name, slug, timezone, status)
SELECT c.id, r.id, 'Михайловск', 'mihaylovsk', 'Europe/Moscow', 'pilot'
  FROM countries c
  JOIN regions r ON r.country_id = c.id AND r.name = 'Ставропольский край'
 WHERE c.code = 'RU'
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- Зарезервированные ники. Список нужен ДО запуска: потом их разберут.
-- ---------------------------------------------------------------------------

INSERT INTO reserved_nicknames (nickname_normalized, reason)
SELECT normalize_text(v), 'system'
  FROM (VALUES
    ('admin'), ('administrator'), ('root'), ('support'), ('help'),
    ('moderator'), ('moder'), ('staff'), ('team'), ('official'),
    ('system'), ('bot'), ('api'), ('null'), ('undefined'),
    ('me'), ('you'), ('everyone'), ('all'),
    ('админ'), ('администратор'), ('поддержка'), ('модератор'),
    ('система'), ('бот'), ('все')
  ) AS t(v)
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- Фича-флаги. Всё, чего нет в MVP, выключено, но инфраструктура заложена.
-- ---------------------------------------------------------------------------

INSERT INTO feature_flags (key, description, enabled_globally) VALUES
    ('subscriptions',      'Платные тарифы игрока и капитана',        false),
    ('economy',            'Очки, кредиты, косметика',                false),
    ('lootboxes',          'Лутбоксы как награда за задания',         false),
    ('predictions',        'Прогнозы на матчи за очки',               false),
    ('competitions',       'Турниры',                                 false),
    ('city_tables',        'Городские таблицы и рейтинги',            false),
    ('challenges',         'Вызовы между командами',                  true),
    ('match_comments',     'Комментарии под матчем',                  true),
    ('payment_flags',      'Метки спорных расчётов',                  false),
    ('ads',                'Рекламные блоки',                         false),
    ('gk_rotation',        'Сменный вратарь',                         true)
ON CONFLICT (key) DO NOTHING;

-- ---------------------------------------------------------------------------
-- Достижения. Ветка reliability — самая важная: она превращает скучное
-- «приходи вовремя» в то, что можно показать.
-- ---------------------------------------------------------------------------

INSERT INTO achievements (code, branch, name, description, threshold, tier, points_award) VALUES
    ('played_10',      'participation', 'Свой на районе',    'Сыграно 10 матчей',                    10, 1, 100),
    ('played_50',      'participation', 'Завсегдатай',       'Сыграно 50 матчей',                    50, 2, 300),
    ('played_100',     'participation', 'Сотка',             'Сыграно 100 матчей',                  100, 3, 700),
    ('played_250',     'participation', 'Легенда коробки',   'Сыграно 250 матчей',                  250, 4, 2000),
    ('winter_match',   'participation', 'Мороз не помеха',   'Матч при температуре ниже нуля',     NULL, 1, 150),

    ('first_goal',     'sport',         'Первый гол',        'Забит первый мяч',                      1, 1, 50),
    ('hattrick',       'sport',         'Хет-трик',          'Три гола в одном матче',                3, 2, 200),
    ('clean_sheet_3',  'sport',         'Сухая серия',       'Три матча подряд на воротах без пропущенных', 3, 2, 250),
    ('city_top_scorer','sport',         'Золотой мяч города','Лучший бомбардир города за сезон',   NULL, 4, 2000),

    ('streak_20',      'reliability',   'Железный',          '20 матчей подряд без неявок',          20, 3, 500),
    ('no_late_season', 'reliability',   'Всегда вовремя',    'Сезон без единого опоздания',        NULL, 3, 400),
    ('never_missed_10','reliability',   'Слово держит',      '10 матчей подряд без неявок',          10, 2, 200),

    ('organized_10',   'organizer',     'Собиратель',        'Создано 10 матчей',                    10, 1, 150),
    ('organized_50',   'organizer',     'Двигатель района',  'Создано 50 матчей',                    50, 3, 600),
    ('brought_5',      'organizer',     'Зазывала',          'Приведено 5 новых игроков',             5, 2, 400),
    ('season_no_cancel','organizer',    'Ни одной отмены',   'Сезон без отменённых матчей',        NULL, 3, 600)
ON CONFLICT (code) DO NOTHING;

-- ---------------------------------------------------------------------------
-- Задания сезонного пропуска
-- ---------------------------------------------------------------------------

INSERT INTO quests (code, name, description, period, target, reward_points) VALUES
    ('weekly_play_1',   'Выйти на поле',      'Сыграй хотя бы один матч на этой неделе',  'weekly',  1, 50),
    ('monthly_play_4',  'Четыре за месяц',    'Сыграй 4 матча в этом месяце',             'monthly', 4, 200),
    ('monthly_no_miss', 'Без пропусков',      'Ни одной неявки за месяц',                 'monthly', 1, 300),
    ('monthly_bring_1', 'Приведи друга',      'Приведи нового игрока, который сыграл матч','monthly', 1, 400)
ON CONFLICT (code) DO NOTHING;

-- +goose Down

DELETE FROM quests WHERE code IN
    ('weekly_play_1','monthly_play_4','monthly_no_miss','monthly_bring_1');
DELETE FROM achievements WHERE code IN
    ('played_10','played_50','played_100','played_250','winter_match',
     'first_goal','hattrick','clean_sheet_3','city_top_scorer',
     'streak_20','no_late_season','never_missed_10',
     'organized_10','organized_50','brought_5','season_no_cancel');
DELETE FROM feature_flags WHERE key IN
    ('subscriptions','economy','lootboxes','predictions','competitions',
     'city_tables','challenges','match_comments','payment_flags','ads','gk_rotation');
DELETE FROM reserved_nicknames;
DELETE FROM cities WHERE slug IN ('stavropol', 'mihaylovsk');
DELETE FROM regions WHERE name = 'Ставропольский край';
DELETE FROM countries WHERE code = 'RU';
