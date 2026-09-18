#!/bin/sh
set -eu
vault secrets enable -path=mongodb database >/dev/null
vault write mongodb/config/test plugin_name=mongodb-database-plugin allowed_roles=ror-api \
  connection_url='mongodb://{{username}}:{{password}}@mongodb:27017/' username=test password=synthetic-only >/dev/null
vault write mongodb/roles/ror-api db_name=test \
  creation_statements='{"db":"nhn-ror","roles":[{"role":"readWrite","db":"nhn-ror"}]}' \
  default_ttl=1h max_ttl=2h >/dev/null
vault secrets enable rabbitmq >/dev/null
vault write rabbitmq/config/connection connection_uri=http://rabbitmq:15672 username=test password=synthetic-only >/dev/null
vault write rabbitmq/roles/ror-api vhosts='{"/":{"configure":".*","write":".*","read":".*"}}' >/dev/null
vault secrets enable -path=database database >/dev/null
vault write database/config/valkey plugin_name=redis-database-plugin host=valkey port=6379 tls=false \
  username=default password=synthetic-only allowed_roles=valkey-ror-api-role >/dev/null
vault write database/roles/valkey-ror-api-role db_name=valkey creation_statements='["~*", "&*", "+@all"]' default_ttl=1h max_ttl=2h >/dev/null
vault kv put secret/v1.0/ror/config/common apikeySalt=synthetic-only >/dev/null
printf '%s' '{"domainResolvers":[]}' | vault kv put secret/v1.0/ror/config/auth - >/dev/null