# Выдержки GeoNames для тестов пакета source

Данные: GeoNames, https://www.geonames.org — лицензия CC BY 4.0
(https://creativecommons.org/licenses/by/4.0/). Выгрузка от 02.10.2026. Изменения: отбор строк,
сокращение колонки; формат не менялся.

- `RU.txt` — 6 строк основного файла `export/dump/RU.zip` (запись `RU.txt`), 19 колонок через TAB:
  Ставрополь (487846, PPLA), Михайловск Ставропольский (493702, PPLA2), Михайловск Свердловский
  (526815, PPL, регион 71), тёзка-деревня без населения (802557, регион 70), Зюзино (461740, PPLX —
  район Москвы), Орловка (12628120, PPLQ, пустая таймзона). Колонка 4 (`alternatenames`) сокращена
  до трёх первых названий.
- `alternateNamesV2-RU.txt` — 46 строк `export/dump/alternatenames/RU.zip` (запись `RU.txt`),
  10 колонок (alternateNameId, geonameid, isolanguage, alternate name, isPreferredName, isShortName,
  isColloquial, isHistoric, from, to), без изменений: названия мест выше, регионов 487839
  (Ставропольский край) и 1490542 (Свердловская область) на языках ru, en, de, без языка, link;
  плюс две строки с `from`/`to` (11389239, 1738212).
- `admin1CodesASCII.txt` — 3 строки `export/dump/admin1CodesASCII.txt` (code, name, name ascii,
  geonameid) без изменений: RU.48 (Moscow), RU.70 (Stavropol Kray), RU.71 (Sverdlovsk Oblast).
  Сверено с настоящим файлом 05.10.2026: 3865 строк, все по 4 колонки, без `\r`.

Файлы содержат хвостовые TAB (пустые колонки) — редактор не должен их срезать (`.editorconfig`).
