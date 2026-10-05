package archtest

import (
	"fmt"
	"path"
	"strings"

	"go.yaml.in/yaml/v3"

	"wf/backend/internal/archtest/sqlscan"
)

// Kind — вид объекта схемы.
type Kind int

const (
	Table Kind = iota
	View
	Function
)

func (k Kind) String() string { return [...]string{"table", "view", "function"}[k] }

// Object — объект схемы и его модуль-владелец (спека бэкенда §4.1). Пишет объект только
// владелец; чужой модуль читает только экспортированные представления и вызывает только
// экспортированные функции (§4.2).
type Object struct {
	Kind     Kind
	Owner    string
	Exported bool
}

func table(owner string) Object { return Object{Kind: Table, Owner: owner} }

// Schema — владельцы всех объектов схемы public, кроме объектов расширений (PostGIS и др.)
// и служебных объектов River (ownerByPrefix). Новая таблица без записи здесь роняет
// TestEverySchemaObjectHasOwner, удалённая — тоже.
var Schema = map[string]Object{
	// platform
	"audit_log":        table("platform"),
	"outbox":           table("platform"),
	"event_inbox":      table("platform"),
	"event_cursors":    table("platform"),
	"idempotency_keys": table("platform"),
	"rate_limits":      table("platform"),
	"humancheck_spent": table("platform"),
	"feature_flags":    table("platform"),
	"app_versions":     table("platform"),
	"goose_db_version": table("platform"),

	"normalize_text":           {Function, "platform", true},
	"immutable_unaccent":       {Function, "platform", true},
	"age_years":                {Function, "platform", true},
	"set_updated_at":           {Function, "platform", false},
	"audit_log_is_append_only": {Function, "platform", false},
	"outbox_notify":            {Function, "platform", false},

	// geo (спека geo §2)
	"countries":              table("geo"),
	"regions":                table("geo"),
	"cities":                 table("geo"),
	"districts":              table("geo"),
	"country_names":          table("geo"),
	"region_names":           table("geo"),
	"city_names":             table("geo"),
	"city_slug_history":      table("geo"),
	"geonames_imports":       table("geo"),
	"geonames_places":        table("geo"),
	"geonames_place_names":   table("geo"),
	"geonames_admin1":        table("geo"),
	"geonames_admin1_names":  table("geo"),
	"geo_read_city_settings": {View, "geo", true},

	// identity
	"users":                 table("identity"),
	"sessions":              table("identity"),
	"role_assignments":      table("identity"),
	"nickname_reservations": table("identity"),
	"nickname_changes":      table("identity"),
	"reserved_nicknames":    table("identity"),

	// pitches
	"pitches":        table("pitches"),
	"nearby_pitches": {Function, "pitches", true},

	// teams
	"teams":                table("teams"),
	"team_slug_history":    table("teams"),
	"team_members":         table("teams"),
	"team_join_requests":   table("teams"),
	"captaincy_transfers":  table("teams"),
	"captain_applications": table("teams"),

	// matches
	"matches":                            table("matches"),
	"match_slots":                        table("matches"),
	"match_participants":                 table("matches"),
	"match_comments":                     table("matches"),
	"challenges":                         table("matches"),
	"challenge_responses":                table("matches"),
	"position_reservations":              table("matches"),
	"matches_set_slot":                   {Function, "matches", false},
	"position_reservations_revoke_limit": {Function, "matches", false},

	// results
	"match_reports": table("results"),
	"match_events":  table("results"),
	"match_votes":   table("results"),
	"match_ratings": table("results"),

	// stats
	"player_stats_daily": table("stats"),
	"team_stats_daily":   table("stats"),
	"team_ratings":       table("stats"),
	"team_stat_resets":   table("stats"),

	// reputation
	"reliability_snapshots":      table("reputation"),
	"reliability_reset_requests": table("reputation"),
	"payment_notes":              table("reputation"),
	"payment_flags":              table("reputation"),
	"payment_flag_disputes":      table("reputation"),

	// economy
	"wallets":           table("economy"),
	"ledger_entries":    table("economy"),
	"subscriptions":     table("economy"),
	"catalog_items":     table("economy"),
	"user_inventory":    table("economy"),
	"team_inventory":    table("economy"),
	"achievements":      table("economy"),
	"user_achievements": table("economy"),
	"lootbox_types":     table("economy"),
	"lootbox_grants":    table("economy"),
	"quests":            table("economy"),
	"user_quests":       table("economy"),
	"referrals":         table("economy"),

	// competitions, predictions
	"competitions":      table("competitions"),
	"competition_teams": table("competitions"),
	"predictions":       table("predictions"),

	// moderation
	"abuse_reports": table("moderation"),
	"sanctions":     table("moderation"),
	"anomaly_flags": table("moderation"),

	// notify
	"notifications":            table("notify"),
	"notification_preferences": table("notify"),
	"notification_settings":    table("notify"),
	"push_tokens":              table("notify"),

	// ads
	"ad_placements": table("ads"),
	"ad_dismissals": table("ads"),
}

// ownerByPrefix — объекты, которые создаёт не наш код: миграции River (набор таблиц
// и функций меняется от версии к версии).
var ownerByPrefix = map[string]string{"river_": "platform"}

func ownerOf(name string) (Object, bool) {
	if o, ok := Schema[name]; ok {
		return o, true
	}
	for prefix, owner := range ownerByPrefix {
		if strings.HasPrefix(name, prefix) {
			return Object{Kind: Table, Owner: owner}, true
		}
	}
	return Object{}, false
}

// ownershipViolations — нарушения владения в SQL модуля module (спека §4.1–4.2).
func ownershipViolations(module string, u sqlscan.Usage) []string {
	var out []string
	for _, name := range u.Writes {
		o, ok := ownerOf(name)
		switch {
		case !ok:
			out = append(out, fmt.Sprintf("пишет %s — объекта нет в archtest.Schema", name))
		case o.Kind != Table:
			out = append(out, fmt.Sprintf("пишет в %s — это не таблица", name))
		case o.Owner != module:
			out = append(out, fmt.Sprintf("пишет чужую таблицу %s (владелец %s)", name, o.Owner))
		}
	}
	for _, name := range u.Reads {
		o, ok := ownerOf(name)
		switch {
		case !ok:
			out = append(out, fmt.Sprintf("читает %s — объекта нет в archtest.Schema", name))
		case o.Owner == module, o.Kind == View && o.Exported:
		default:
			out = append(out, fmt.Sprintf("читает %s модуля %s — чужое читается только из экспортированного представления (спека §4.2)", name, o.Owner))
		}
	}
	for _, name := range u.Functions {
		o, ok := Schema[name]
		if !ok || o.Kind != Function {
			continue // встроенные функции, функции расширений и River
		}
		if o.Owner != module && !o.Exported {
			out = append(out, fmt.Sprintf("вызывает %s модуля %s — функция не экспортирована", name, o.Owner))
		}
	}
	return out
}

// queryGlobs — где страж владения ищет SQL модулей (пути от internal/).
var queryGlobs = []string{"*/queries/*.sql", "platform/*/queries/*.sql"}

// sqlcQueriesViolations сверяет backend/sqlc.yaml со стражем владения: каждый queries —
// каталог internal/<модуль>/queries/ (модуль из Layers) или internal/platform/<пакет>/queries/,
// и queryGlobs его покрывают. dirs — сколько путей queries проверено.
func sqlcQueriesViolations(cfg []byte) (dirs int, violations []string, err error) {
	var c struct {
		SQL []struct {
			Queries any `yaml:"queries"`
		} `yaml:"sql"`
	}
	if err := yaml.Unmarshal(cfg, &c); err != nil {
		return 0, nil, fmt.Errorf("sqlc.yaml: %w", err)
	}
	for i, entry := range c.SQL {
		var paths []string
		switch q := entry.Queries.(type) {
		case string:
			paths = []string{q}
		case []any:
			for _, item := range q {
				s, ok := item.(string)
				if !ok {
					return 0, nil, fmt.Errorf("sqlc.yaml: sql[%d].queries: %v — не строка", i, item)
				}
				paths = append(paths, s)
			}
		case nil:
			violations = append(violations, fmt.Sprintf("sqlc.yaml: sql[%d] без queries", i))
		default:
			return 0, nil, fmt.Errorf("sqlc.yaml: sql[%d].queries: неожиданный тип %T", i, q)
		}
		for _, q := range paths {
			dirs++
			if v := queriesDirViolation(q); v != "" {
				violations = append(violations, fmt.Sprintf("sqlc.yaml: sql[%d].queries %s: %s", i, q, v))
			}
		}
	}
	return dirs, violations, nil
}

func queriesDirViolation(q string) string {
	p := path.Clean(strings.ReplaceAll(q, `\`, "/"))
	seg := strings.Split(p, "/")
	switch {
	case len(seg) == 3 && seg[0] == "internal" && seg[2] == "queries":
		if _, ok := Layers[seg[1]]; !ok {
			return fmt.Sprintf("%s — не модуль из archtest.Layers", seg[1])
		}
	case len(seg) == 4 && seg[0] == "internal" && seg[1] == "platform" && seg[3] == "queries":
	default:
		return "SQL модуля — только в internal/<модуль>/queries/ или internal/platform/<пакет>/queries/ (каталог), иначе страж владения его не увидит"
	}
	rel := strings.TrimPrefix(p, "internal/") + "/probe.sql"
	for _, g := range queryGlobs {
		if ok, _ := path.Match(g, rel); ok {
			return ""
		}
	}
	return "каталог не покрыт queryGlobs стража владения"
}
