#!/usr/bin/env bash
# scripts/s3/init.sh — настройка dev-Garage; зовёт `scripts/s3.sh up`, повторный запуск безопасен.
# Слой из одного узла, бакеты wf-in, wf-pub, wf-priv (R1), ключи процессов с правами по R2, сайт wf-pub (R5),
# жизненный цикл wf-in 1 сутки и CORS wf-in для dev-фронтов. Lifecycle и CORS ставятся только S3 API ключом с
# правом owner — служебный ключ wf-dev-setup живёт только здесь и удаляется в конце (наружу не выдаётся).
# Вход: WF_S3_CONTAINER (имя контейнера), WF_S3_ENDPOINT (адрес S3 API на хосте). Секреты не печатаются.
set -euo pipefail
export MSYS_NO_PATHCONV=1

NAME=${WF_S3_CONTAINER:?WF_S3_CONTAINER не задан}
EP=${WF_S3_ENDPOINT:?WF_S3_ENDPOINT не задан}
REGION=garage
# dev-фронты: веб (next dev --port 3000) и админка (vite, 5173)
ORIGINS=(http://localhost:3000 http://127.0.0.1:3000 http://localhost:5173 http://127.0.0.1:5173)

die() { echo "s3/init.sh: $*" >&2; exit 1; }
g() { docker exec -e RUST_LOG=warn "$NAME" /garage "$@"; }
field() { awk -v f="$1" -F': *' '$1 == f { print $2; exit }'; }

st=$(g status)
if [[ $st == *'NO ROLE ASSIGNED'* ]]; then
  node=$(g node id -q | cut -d@ -f1)
  g layout assign -z dc1 -c 1G "$node" >/dev/null
  g layout apply --version 1 >/dev/null
fi

for b in wf-in wf-pub wf-priv; do
  g bucket info "$b" >/dev/null 2>&1 || g bucket create "$b" >/dev/null
done
# Служебный ключ с owner на wf-in не переживает скрипт и при сбое (ревью Task 3, m4): удаление — по выходу.
trap 'g key delete --yes wf-dev-setup >/dev/null 2>&1 || true' EXIT
# `key create` печатает секрет — вывод не показываем; имя ключа уникально только по нашей проверке
for k in wf-dev-api wf-dev-admin wf-dev-worker wf-dev-setup; do
  g key info "$k" >/dev/null 2>&1 || g key create "$k" >/dev/null
done

# R2: write включает удаление, read — листинг
for k in wf-dev-api wf-dev-admin; do
  g bucket allow --read --write wf-in --key "$k" >/dev/null
  g bucket allow --read wf-priv --key "$k" >/dev/null
done
for b in wf-in wf-pub wf-priv; do
  g bucket allow --read --write "$b" --key wf-dev-worker >/dev/null
done
g bucket website --allow wf-pub >/dev/null
g bucket allow --owner wf-in --key wf-dev-setup >/dev/null

info=$(g key info --show-secret wf-dev-setup)
setup_id=$(printf '%s\n' "$info" | field 'Key ID')
setup_secret=$(printf '%s\n' "$info" | field 'Secret key')
[ -n "$setup_id" ] && [ -n "$setup_secret" ] || die "не разобран вывод garage key info для wf-dev-setup"

# put_config <подресурс> <xml> — подписанный PUT в wf-in; ключ — через конфиг curl на stdin, не в argv
put_config() {
  local md5 sha
  md5=$(printf '%s' "$2" | openssl md5 -binary | openssl base64)
  # хэш тела — явно: старые curl (< 8.x) не добавляют x-amz-content-sha256 для s3 сами
  sha=$(printf '%s' "$2" | openssl dgst -sha256 -r | cut -d' ' -f1)
  printf 'user = "%s:%s"\n' "$setup_id" "$setup_secret" |
    curl -sS --fail-with-body --retry 5 --retry-all-errors --retry-delay 1 -K - \
      --aws-sigv4 "aws:amz:$REGION:s3" -X PUT \
      -H "Content-MD5: $md5" -H "x-amz-content-sha256: $sha" -H 'Content-Type: application/xml' \
      --data-binary "$2" "$EP/wf-in?$1" >/dev/null ||
    die "PUT wf-in?$1 не прошёл"
}

# get_config <подресурс> — подписанный GET wf-in?<подресурс>, тело ответа — в stdout (хэш пустого тела — явно)
get_config() {
  printf 'user = "%s:%s"\n' "$setup_id" "$setup_secret" |
    curl -sS --fail-with-body --retry 5 --retry-all-errors --retry-delay 1 -K - \
      --aws-sigv4 "aws:amz:$REGION:s3" \
      -H 'x-amz-content-sha256: e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855' "$EP/wf-in?$1" ||
    die "GET wf-in?$1 не прошёл"
}

put_config lifecycle '<LifecycleConfiguration><Rule><ID>wf-in-1d</ID><Status>Enabled</Status><Filter><Prefix></Prefix></Filter><Expiration><Days>1</Days></Expiration></Rule></LifecycleConfiguration>'
# PUT мог ответить 2xx и не применить правило (ревью Task 3, m5): жизненный цикл — страховка чистки wf-in (спека
# §6.2), поэтому сверяем то, что хранилище отдаёт обратно
lc=$(get_config lifecycle)
[[ $lc == *'<ID>wf-in-1d</ID>'* && $lc == *'<Status>Enabled</Status>'* && $lc == *'<Days>1</Days>'* ]] ||
  die "lifecycle wf-in не применён: хранилище отдаёт «${lc:0:300}»"

cors='<CORSConfiguration><CORSRule>'
for o in "${ORIGINS[@]}"; do cors+="<AllowedOrigin>$o</AllowedOrigin>"; done
cors+='<AllowedMethod>POST</AllowedMethod><AllowedHeader>*</AllowedHeader><MaxAgeSeconds>3600</MaxAgeSeconds></CORSRule></CORSConfiguration>'
put_config cors "$cors"

g key delete --yes wf-dev-setup >/dev/null
trap - EXIT
