// Package hosttest is the stable test-support surface for external gouploads
// consumers.
//
// Host projects may use this package from tests and test helpers when they need
// to assert values that are otherwise owned by gouploads internals.
//
// Database integration helpers are available only when consumers build with the
// integration tag. They expect the host test process to provide TEST_PSQL_*
// environment variables matching the PostgreSQL test database.
package hosttest
