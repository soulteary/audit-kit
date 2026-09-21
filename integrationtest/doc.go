// Package integrationtest holds the tests that talk to a real PostgreSQL or
// MySQL server. They are behind the "integration" build tag and skip unless
// TEST_POSTGRES_URL or TEST_MYSQL_URL is set:
//
//	TEST_POSTGRES_URL=postgres://… go test -tags=integration ./integrationtest/...
//
// They live outside the root package for the same reason the root package
// stopped importing drivers: a driver imported by a package's test still
// reaches every consumer's go.sum. Here it reaches nobody, and the tests get
// to register their drivers exactly as a real program would.
package integrationtest
