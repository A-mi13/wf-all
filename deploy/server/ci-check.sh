#!/usr/bin/env bash
# Скрипт шага «подъём и исполнение фона» job `server` в .github/workflows/image.yml (запуск: bash ci-check.sh из
# deploy/server). Вынесен из YAML: actionlint 1.7.12 на Windows виснет на run: длиннее ~4 КиБ (issue #650).
# Только -e, как у run: без shell (bash -e {0}): pipefail сломал бы `logs | grep -q` (SIGPIPE у logs).
set -e
fail() { echo "::error::server ($run): $1"; exit 1; }
# журналы стека — при любом ненулевом выходе
# shellcheck disable=SC2154 # rc присваивается в той же строке trap; actionlint исключал SC2154 и в run:
trap 'rc=$?; [ "$rc" -eq 0 ] || docker compose "${files[@]}" logs --no-color || true; exit "$rc"' EXIT
# check <сервис с фоном>: /readyz через Caddy, журнал «воркер запущен», исполнилась периодика
check() {
  local bg=$1 ok=
  # migrate — первым и успешно (up --wait ждёт здоровья, migrate — выхода)
  docker compose "${files[@]}" up -d --wait --wait-timeout 180 || fail "up --wait"
  [ "$(docker inspect -f '{{.State.ExitCode}}' "$(docker compose "${files[@]}" ps -aq migrate)")" = 0 ] ||
    fail "migrate не 0"
  docker compose "${files[@]}" exec -T caddy cat /data/caddy/pki/authorities/local/root.crt >root.crt
  for host in api.wf.test admin.wf.test; do
    ok=
    for _ in $(seq 1 30); do
      if curl -fsS --cacert root.crt --resolve "$host:443:127.0.0.1" "https://$host/readyz"; then
        ok=1
        break
      fi
      sleep 2
    done
    [ -n "$ok" ] || fail "https://$host/readyz за 60 с"
  done
  ok=
  for _ in $(seq 1 30); do
    if docker compose "${files[@]}" logs --no-color "$bg" | grep -q 'воркер запущен'; then
      ok=1
      break
    fi
    sleep 2
  done
  [ -n "$ok" ] || fail "в журнале $bg нет «воркер запущен» за 60 с"
  if [ "$bg" = api ]; then
    docker compose "${files[@]}" logs --no-color api | grep 'воркер запущен' | grep -q '"component":"worker"' ||
      fail "«воркер запущен» у api без component=worker"
  fi
  # выборы лидера и RunOnStart периодики — до 120 с
  ok=
  for _ in $(seq 1 60); do
    # || true: под bash -e сбой exec (db ещё не принимает) оборвал бы цикл — это повтор, не отказ
    n=$(docker compose "${files[@]}" exec -T db psql -U postgres -d wf -tAc \
      "SELECT count(*) FROM river_job WHERE kind = 'ratelimit.cleanup' AND state = 'completed'") || true
    if [ "${n:-0}" -ge 1 ]; then ok=1; break; fi
    sleep 2
  done
  [ -n "$ok" ] || fail "ratelimit.cleanup не дошла до completed за 120 с"
}
# Хранилище (спека media §6.5, R3, R5): загрузка подписанным POST (ключ api) через адрес хранилища, копия в
# wf-pub ключом воркера — как публикует вариант воркер, чтение через домен медиа; запреты обоих сайтов;
# повторный storage-init — без ошибок. Подпись POST — SigV4 политики (как blob/s3.PresignUpload).
storage_check() {
  local c=(-sS --cacert root.crt --resolve media.wf.test:443:127.0.0.1 --resolve storage.wf.test:443:127.0.0.1)
  local key=ci-check/hello.txt payload='wf storage ci' h
  # hc <curl-аргументы> — HTTP-код ответа; обрыв соединения — 000, а не выход bash -e мимо fail
  hc() { curl "${c[@]}" -o /dev/null -w '%{http_code}' "$@" || true; }
  hmac() { printf '%s' "$2" | openssl dgst -sha256 -mac HMAC -macopt "$1" -r | cut -d' ' -f1; }
  local ak sk d amz exp cred policy k sig
  ak=$(sed -n 's/^API_BLOB_S3_ACCESS_KEY=//p' api.blob.env)
  sk=$(sed -n 's/^API_BLOB_S3_SECRET_KEY=//p' api.blob.env)
  d=$(date -u +%Y%m%d) amz=$(date -u +%Y%m%dT%H%M%SZ) exp=$(date -u -d '+10 min' +%Y-%m-%dT%H:%M:%SZ)
  cred="$ak/$d/garage/s3/aws4_request"
  policy=$(printf '{"expiration":"%s","conditions":[{"bucket":"wf-in"},{"key":"%s"},{"Content-Type":"text/plain"},%s,{"x-amz-algorithm":"AWS4-HMAC-SHA256"},{"x-amz-credential":"%s"},{"x-amz-date":"%s"}]}' \
    "$exp" "$key" "[\"content-length-range\",${#payload},${#payload}]" "$cred" "$amz" | base64 -w0)
  k=$(hmac "key:AWS4$sk" "$d")
  for part in garage s3 aws4_request; do k=$(hmac "hexkey:$k" "$part"); done
  sig=$(hmac "hexkey:$k" "$policy")
  printf '%s' "$payload" >hello.txt
  # браузер шлёт multipart POST формы без предзапроса: CORS нужен на ответе самого POST, а не только на
  # OPTIONS (ревью Task 12, m2); заголовки и код — одним выводом, код последней строкой
  h=$(curl "${c[@]}" -D - -o /dev/null -w '%{http_code}' -H 'Origin: https://wf.test' \
    --form-string "key=$key" --form-string "policy=$policy" \
    --form-string 'x-amz-algorithm=AWS4-HMAC-SHA256' --form-string "x-amz-credential=$cred" \
    --form-string "x-amz-date=$amz" --form-string "x-amz-signature=$sig" \
    --form-string 'Content-Type=text/plain' -F 'file=@hello.txt;type=text/plain' https://storage.wf.test/wf-in) ||
    h=000
  [[ ${h##*$'\n'} == 2?? ]] || fail "POST загрузки в wf-in через адрес хранилища: ${h##*$'\n'}"
  grep -qi '^access-control-allow-origin: https://wf.test' <<<"$h" ||
    fail "POST загрузки в wf-in: нет Access-Control-Allow-Origin на ответе"
  # публикация: чтение из wf-in и запись в wf-pub ключом воркера изнутри сети wf (S3-порт наружу закрыт);
  # копия в wf-priv — для подписанного чтения закрытого ниже
  docker compose "${files[@]}" run --rm --no-deps -T --entrypoint sh storage-init -c '
    set -eu
    cfg() { printf "user = \"%s:%s\"\n" "$WORKER_BLOB_S3_ACCESS_KEY" "$WORKER_BLOB_S3_SECRET_KEY"; }
    cfg | curl -fsS -K - --aws-sigv4 aws:amz:garage:s3 -o /tmp/f http://storage:3900/wf-in/ci-check/hello.txt
    for b in wf-pub wf-priv; do
      cfg | curl -fsS -K - --aws-sigv4 aws:amz:garage:s3 -X PUT --data-binary @/tmp/f \
        -H "Content-Type: text/plain" -H "Cache-Control: public, max-age=86400" \
        "http://storage:3900/$b/ci-check/hello.txt"
    done' || fail "копия в wf-pub и wf-priv ключом воркера"
  # закрытое: подписанный GET через адрес хранилища (query-SigV4 ключом api, как blob/s3.PresignGet) — 200 и
  # тело; подпись включает Host, поэтому подмена Host в Caddy ломает ссылки (ревью финала B, m1); подделанная
  # подпись — отказ хранилища
  local q creq sts gsig
  q="X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Credential=$ak%2F$d%2Fgarage%2Fs3%2Faws4_request&X-Amz-Date=$amz"
  q+="&X-Amz-Expires=300&X-Amz-SignedHeaders=host"
  creq=$(printf 'GET\n/wf-priv/%s\n%s\nhost:storage.wf.test\n\nhost\nUNSIGNED-PAYLOAD' "$key" "$q")
  sts=$(printf 'AWS4-HMAC-SHA256\n%s\n%s/garage/s3/aws4_request\n%s' "$amz" "$d" \
    "$(printf '%s' "$creq" | openssl dgst -sha256 -r | cut -d' ' -f1)")
  gsig=$(hmac "hexkey:$k" "$sts")
  h=$(curl "${c[@]}" -o got.txt -w '%{http_code}' "https://storage.wf.test/wf-priv/$key?$q&X-Amz-Signature=$gsig") ||
    h=000
  [ "$h" = 200 ] && [ "$(cat got.txt)" = "$payload" ] ||
    fail "подписанный GET /wf-priv/$key через адрес хранилища: $h $(head -c 300 got.txt 2>/dev/null)"
  if [ "${gsig: -1}" = 0 ]; then gsig=${gsig%?}1; else gsig=${gsig%?}0; fi
  h=$(hc "https://storage.wf.test/wf-priv/$key?$q&X-Amz-Signature=$gsig")
  [[ $h == 4?? && $h != 404 ]] || fail "GET /wf-priv/$key с подделанной подписью: $h, ждали отказ хранилища"
  # домен медиа: тело, nosniff, Cache-Control и тип из метаданных объекта
  h=$(curl "${c[@]}" -f -D - -o got.txt "https://media.wf.test/$key") || fail "GET /$key через домен медиа"
  [ "$(cat got.txt)" = "$payload" ] || fail "GET /$key через домен медиа: другое тело"
  for want in '^x-content-type-options: nosniff' '^cache-control: public, max-age=86400' \
    '^content-type: text/plain'; do
    grep -qi "$want" <<<"$h" || fail "GET /$key через домен медиа: нет заголовка $want"
  done
  [ "$(hc -I "https://media.wf.test/$key")" = 200 ] || fail "HEAD /$key через домен медиа"
  for m in DELETE PUT POST; do
    h=$(hc -X "$m" "https://media.wf.test/$key")
    [ "$h" = 405 ] || fail "$m /$key через домен медиа: $h, ждали 405"
  done
  # входящие и закрытые через домен медиа не видны, даже существующий файл wf-in
  for p in / "/wf-in/$key" "/wf-priv/$key"; do
    h=$(hc "https://media.wf.test$p")
    [ "$h" = 404 ] || fail "GET $p через домен медиа: $h, ждали 404"
  done
  # адрес хранилища: ни листинга, ни чтения без подписи, ни чужих методов
  for p in / /wf-in "/wf-in/$key" "/wf-priv/$key" "/wf-pub/$key"; do
    h=$(hc "https://storage.wf.test$p")
    [ "$h" = 404 ] || fail "GET $p через адрес хранилища: $h, ждали 404"
  done
  h=$(hc -X DELETE "https://storage.wf.test/wf-in")
  [ "$h" = 404 ] || fail "DELETE /wf-in через адрес хранилища: $h, ждали 404"
  # CORS загрузки с веба: предзапрос проходит до Garage и получает origin из WF_WEB_ORIGIN
  h=$(curl "${c[@]}" -D - -o /dev/null -X OPTIONS -H 'Origin: https://wf.test' \
    -H 'Access-Control-Request-Method: POST' https://storage.wf.test/wf-in) || h=
  grep -qi '^access-control-allow-origin: https://wf.test' <<<"$h" || fail "CORS предзапрос POST /wf-in"
  docker compose "${files[@]}" run --rm --no-deps -T storage-init || fail "повторный storage-init"
}
run=worker files=(-f compose.yaml -f compose.ci.yaml)
check worker
storage_check
docker compose "${files[@]}" down -v
run=embed files=(-f compose.yaml -f compose.ci.yaml -f compose.ci-embed.yaml)
if docker compose "${files[@]}" config --services | grep -qx worker; then fail "сервис worker не выключен"; fi
check api
docker compose "${files[@]}" down -v
