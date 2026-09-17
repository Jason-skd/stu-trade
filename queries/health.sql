-- name: GetSchemaVersion :one
SELECT version
FROM app_schema
WHERE singleton;
