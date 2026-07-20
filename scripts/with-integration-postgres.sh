#!/bin/sh

set -eu

# Explicit caller configuration always wins. This is the path used by CI and
# by developers with a non-Compose PostgreSQL topology.
if [ -n "${TEST_PSQL_ADDRESS_LOCAL:-}" ] || [ -n "${TEST_PSQL_PORT_LOCAL:-}" ]; then
	exec "$@"
fi

compose_ids=""
if command -v docker >/dev/null 2>&1; then
	if compose_ids=$(docker ps \
		--filter "label=com.docker.compose.service=integration-postgres-tests" \
		--format '{{.ID}}' 2>/dev/null); then
		:
	else
		compose_ids=""
	fi
fi

if [ -n "$compose_ids" ]; then
	compose_count=$(printf '%s\n' "$compose_ids" | awk 'NF { count++ } END { print count + 0 }')
	if [ "$compose_count" -ne 1 ]; then
		printf '%s\n' \
			"found multiple running integration-postgres-tests Compose services; set TEST_PSQL_ADDRESS_LOCAL and TEST_PSQL_PORT_LOCAL explicitly" >&2
		exit 2
	fi

	container_id=$compose_ids
	published_address=$(docker port "$container_id" 5432/tcp 2>/dev/null | awk 'NR == 1 { print; exit }')
	if [ -n "$published_address" ]; then
		postgres_address=${published_address%:*}
		postgres_port=${published_address##*:}
		postgres_address=${postgres_address#\[}
		postgres_address=${postgres_address%\]}
		case "$postgres_address" in
			0.0.0.0 | ::)
				postgres_address=127.0.0.1
				;;
		esac

		TEST_PSQL_ADDRESS_LOCAL=$postgres_address
		TEST_PSQL_PORT_LOCAL=$postgres_port
		export TEST_PSQL_ADDRESS_LOCAL TEST_PSQL_PORT_LOCAL
		exec "$@"
	fi
fi

# Native host runners commonly expose PostgreSQL directly on its standard
# port. A container runner keeps the test helper's Docker-network defaults.
if [ ! -f /.dockerenv ]; then
	TEST_PSQL_ADDRESS_LOCAL=127.0.0.1
	TEST_PSQL_PORT_LOCAL=5432
	export TEST_PSQL_ADDRESS_LOCAL TEST_PSQL_PORT_LOCAL
fi

exec "$@"
