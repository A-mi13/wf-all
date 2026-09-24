-- name: GetFlag :one
SELECT key, enabled_globally FROM feature_flags WHERE key = $1;
