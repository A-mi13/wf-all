#!/bin/sh
# deploy/server/storage/init.sh — разовая настройка хранилища своего сервера (сервис storage-init в compose.yaml,
# спека media §6.5, план R1, R2, R5). Повторный запуск безопасен: каждый шаг приводит хранилище к одному и тому же
# состоянию (слой узла, бакеты, ключи, права, сайт, lifecycle, CORS), лишние права у ключей процессов снимает.
#
# Всё — через admin API Garage (токен GARAGE_ADMIN_TOKEN, только в сети wf): lifecycle и CORS он ставит сам
# (UpdateBucket), поэтому служебный ключ с правом owner не нужен и не создаётся.
# Ключи процессов — из их env (api.env, admin-api.env, worker.env подключены сервису): оператор генерирует их один раз
# (README, «Хранилище файлов»), init импортирует их в Garage. Единственный источник ключей — env процессов.
# Секреты не печатаются и не попадают в argv: токен — в конфиг curl через stdin, тела запросов — во временный файл.
#
# Образ — curlimages/curl (busybox sh), поэтому POSIX sh без jq: ответы admin API разбираются по полям id и version.
set -eu

ADMIN_URL=${WF_STORAGE_ADMIN_URL:-http://storage:3903}
# Имена бакетов фиксированы: их же знают Caddyfile (пути адреса хранилища, Host сайта) и шаблоны *.env.example
IN=wf-in
PUB=wf-pub
PRIV=wf-priv
# Ёмкость узла в слое: на одном узле — условная (не квота), Garage делит по ней разделы между узлами
CAPACITY=100000000000

die() {
	echo "storage-init: $*" >&2
	exit 1
}

: "${GARAGE_ADMIN_TOKEN:?storage-init: нет GARAGE_ADMIN_TOKEN (storage.env)}"
: "${WF_WEB_ORIGIN:?storage-init: нет WF_WEB_ORIGIN (.env compose)}"

body=$(mktemp)
req=$(mktemp)
trap 'rm -f "$body" "$req"' EXIT
chmod 600 "$body" "$req"

# call МЕТОД ПУТЬ [JSON] — запрос к admin API; тело ответа — в $body, код — в $code. JSON с секретом не попадает в
# argv: пишется в файл $req встроенным printf. Переменные функций sh глобальны — свои имена с «_», чтобы не затирать
# $p циклов.
call() {
	if [ $# -ge 3 ]; then
		printf '%s' "$3" >"$req"
		set -- "$1" "$2" --data-binary "@$req" -H 'Content-Type: application/json'
	fi
	_method=$1
	_path=$2
	shift 2
	code=$(printf 'header = "Authorization: Bearer %s"\n' "$GARAGE_ADMIN_TOKEN" |
		curl -sS -K - -o "$body" -w '%{http_code}' -X "$_method" "$@" "$ADMIN_URL$_path") || code=000
	: >"$req"
}

# must МЕТОД ПУТЬ [JSON] — call, ответ не 2xx — отказ с кодом и началом тела (ошибки admin API секретов не содержат).
must() {
	call "$@"
	case $code in
	2??) ;;
	*) die "$1 ${2%%\?*}: HTTP $code $(snippet)" ;;
	esac
}

# json — тело ответа без пробелов и переводов строк (admin API отдаёт JSON с отступами)
json() { tr -d ' \t\r\n' <"$body"; }

# snippet — начало тела ответа одной строкой, для сообщения об ошибке
snippet() { head -c 300 "$body" | tr '\r\n' '  '; }

# first_id — первое поле "id" (64 hex): id бакета у GetBucketInfo/CreateBucket, id узла у GetClusterStatus.
first_id() { json | grep -o '"id":"[0-9a-f]\{64\}"' | head -n 1 | cut -d'"' -f4; }

# Проверки env до любых изменений: имена бакетов как в Caddyfile, формат ключей, ключи разные у процессов.
# Переменные процесса по префиксу читаются через eval (имя собирается из префикса).
val='' id='' secret=''
for p in API ADMIN WORKER; do
	for pair in "BUCKET_IN:$IN" "BUCKET_PUB:$PUB" "BUCKET_PRIV:$PRIV"; do
		var=${p}_BLOB_S3_${pair%%:*}
		eval "val=\${$var:-}"
		[ "$val" = "${pair#*:}" ] || die "$var = «$val», ждали «${pair#*:}» (имя зашито в Caddyfile и init.sh)"
	done
	eval "id=\${${p}_BLOB_S3_ACCESS_KEY:-}; secret=\${${p}_BLOB_S3_SECRET_KEY:-}"
	printf '%s' "$id" | grep -Eqx 'GK[0-9a-f]{24}' ||
		die "${p}_BLOB_S3_ACCESS_KEY — не ключ Garage (GK + 24 hex: GK\$(openssl rand -hex 12)), README"
	printf '%s' "$secret" | grep -Eqx '[0-9a-f]{64}' ||
		die "${p}_BLOB_S3_SECRET_KEY — не секрет Garage (64 hex: openssl rand -hex 32), README"
done
[ "$API_BLOB_S3_ACCESS_KEY" != "$ADMIN_BLOB_S3_ACCESS_KEY" ] && [ "$API_BLOB_S3_ACCESS_KEY" != "$WORKER_BLOB_S3_ACCESS_KEY" ] &&
	[ "$ADMIN_BLOB_S3_ACCESS_KEY" != "$WORKER_BLOB_S3_ACCESS_KEY" ] ||
	die "ключи api, admin-api и worker должны быть разными (у каждого свои права, R2)"
printf '%s' "$WF_WEB_ORIGIN" | grep -Eqx 'https://[a-z0-9.-]+(:[0-9]+)?' ||
	die "WF_WEB_ORIGIN = «$WF_WEB_ORIGIN» — origin веба вида https://домен (без пути и «/» в конце)"

# admin API отвечает, как только узел поднялся (healthcheck storage — garage status); запас на медленный старт
i=0
until call GET /v2/GetClusterStatus && [ "$code" = 200 ]; do
	i=$((i + 1))
	[ "$i" -lt 30 ] || die "admin API ($ADMIN_URL) не ответил за 30 с: HTTP $code"
	sleep 1
done
node=$(first_id)
[ -n "$node" ] || die "не разобран id узла в GetClusterStatus"

# Слой из одного узла — только на пустом кластере (роль уже есть — не трогаем: смену ёмкости делает оператор)
must GET /v2/GetClusterLayout
if json | grep -q '"roles":\[\]'; then
	ver=$(json | grep -o '"version":[0-9]*' | head -n 1 | cut -d: -f2)
	must POST /v2/UpdateClusterLayout "{\"roles\":[{\"id\":\"$node\",\"zone\":\"dc1\",\"capacity\":$CAPACITY,\"tags\":[]}]}"
	must POST /v2/ApplyClusterLayout "{\"version\":$((ver + 1))}"
	echo "storage-init: слой из одного узла применён"
fi

# bucket_id ИМЯ — id бакета; нет — создать
bucket_id() {
	call GET "/v2/GetBucketInfo?globalAlias=$1"
	case $code in
	200) ;;
	404) must POST /v2/CreateBucket "{\"globalAlias\":\"$1\"}" && echo "storage-init: создан бакет $1" >&2 ;;
	*) die "GetBucketInfo $1: HTTP $code $(snippet)" ;;
	esac
	first_id
}
in_id=$(bucket_id "$IN")
pub_id=$(bucket_id "$PUB")
priv_id=$(bucket_id "$PRIV")
[ -n "$in_id" ] && [ -n "$pub_id" ] && [ -n "$priv_id" ] || die "не разобран id бакета"

# Ключи процессов: есть — секрет должен совпасть с env (иначе процесс получал бы 403); нет — импорт.
for p in API ADMIN WORKER; do
	eval "id=\$${p}_BLOB_S3_ACCESS_KEY; secret=\$${p}_BLOB_S3_SECRET_KEY"
	name=wf-$(printf '%s' "$p" | tr '[:upper:]' '[:lower:]')
	call GET "/v2/GetKeyInfo?id=$id&showSecretKey=true"
	case $code in
	200)
		# сравнение — встроенным case, не grep: секрет не уходит в argv процесса
		case $(json) in
		*"\"secretAccessKey\":\"$secret\""*) ;;
		*) die "ключ $name ($id) уже есть с другим секретом — ${p}_BLOB_S3_SECRET_KEY не тот (ротация — README)" ;;
		esac
		;;
	400 | 404)
		must POST /v2/ImportKey "{\"accessKeyId\":\"$id\",\"secretAccessKey\":\"$secret\",\"name\":\"$name\"}"
		echo "storage-init: импортирован ключ $name"
		;;
	*) die "GetKeyInfo $name: HTTP $code $(snippet)" ;;
	esac
	: >"$body"
done

# grant КЛЮЧ БАКЕТ READ WRITE — ровно эти права (R2: write включает удаление, read — листинг); owner — никому.
# AllowBucketKey только включает флаги, поэтому выключенные снимаются DenyBucketKey.
grant() {
	must POST /v2/AllowBucketKey \
		"{\"bucketId\":\"$2\",\"accessKeyId\":\"$1\",\"permissions\":{\"read\":$3,\"write\":$4,\"owner\":false}}"
	no_read=true no_write=true
	[ "$3" = true ] && no_read=false
	[ "$4" = true ] && no_write=false
	must POST /v2/DenyBucketKey \
		"{\"bucketId\":\"$2\",\"accessKeyId\":\"$1\",\"permissions\":{\"read\":$no_read,\"write\":$no_write,\"owner\":true}}"
}
for k in "$API_BLOB_S3_ACCESS_KEY" "$ADMIN_BLOB_S3_ACCESS_KEY"; do
	grant "$k" "$in_id" true true
	grant "$k" "$pub_id" false false
	grant "$k" "$priv_id" true false
done
for b in "$in_id" "$pub_id" "$priv_id"; do
	grant "$WORKER_BLOB_S3_ACCESS_KEY" "$b" true true
done

# Сайт — только у публичного бакета (домен медиа, R5); входящие и закрытые сайтом не отдаются
must POST "/v2/UpdateBucket?id=$pub_id" '{"websiteAccess":{"enabled":true,"indexDocument":"index.html"}}'
must POST "/v2/UpdateBucket?id=$in_id" '{"websiteAccess":{"enabled":false}}'
must POST "/v2/UpdateBucket?id=$priv_id" '{"websiteAccess":{"enabled":false}}'

# Входящие: жизненный цикл 1 сутки (страховка чистки, спека §3.9) и CORS загрузки POST с веба
must POST "/v2/UpdateBucket?id=$in_id" "{\"lifecycleRules\":[{\"ID\":\"wf-in-1d\",\"Status\":\"Enabled\",\
\"Filter\":{\"Prefix\":\"\"},\"Expiration\":{\"Days\":1}}],\"corsRules\":[{\"AllowedOrigin\":[\"$WF_WEB_ORIGIN\"],\
\"AllowedMethod\":[\"POST\"],\"AllowedHeader\":[\"*\"],\"MaxAgeSeconds\":3600}]}"

echo "storage-init: готово — бакеты $IN, $PUB, $PRIV; ключи wf-api, wf-admin, wf-worker с правами R2"
