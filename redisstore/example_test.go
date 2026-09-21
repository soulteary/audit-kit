package redisstore_test

import (
	"context"
	"fmt"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	audit "github.com/soulteary/audit-kit/v2"
	"github.com/soulteary/audit-kit/v2/redisstore"
)

// A Redis-backed audit log, kept for a week by default.
func Example() {
	// A real program dials its own Redis; miniredis keeps the example runnable.
	server, err := miniredis.Run()
	if err != nil {
		panic(err)
	}
	defer server.Close()

	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer func() { _ = client.Close() }()

	store := redisstore.NewWithConfig(client, &redisstore.Config{
		KeyPrefix: "myservice:audit:",
		TTL:       48 * time.Hour,
	})

	logger := audit.NewLogger(store, audit.DefaultConfig())
	logger.LogAuth(context.Background(), audit.EventLoginFailed, "user-42", audit.ResultFailure,
		audit.WithRecordReason("bad password"),
		audit.WithRecordIP("203.0.113.9"),
	)

	records, err := store.Query(context.Background(), audit.DefaultQueryFilter().WithUserID("user-42"))
	if err != nil {
		panic(err)
	}
	fmt.Println(records[0].EventType, records[0].Reason)

	// Closing the store leaves the client open: the rest of the program is
	// still using it. Config.CloseClient reverses that.
	_ = store.Close()
	fmt.Println(client.Ping(context.Background()).Err() == nil)

	// Output:
	// login_failed bad password
	// true
}

// The store takes any go-redis client shape, so a cluster needs no special
// case.
func ExampleNew_cluster() {
	// NewUniversalClient returns a redis.UniversalClient, which satisfies
	// redisstore.Client just as *redis.Client does.
	client := redis.NewUniversalClient(&redis.UniversalOptions{
		Addrs: []string{"10.0.0.1:6379", "10.0.0.2:6379", "10.0.0.3:6379"},
	})
	defer func() { _ = client.Close() }()

	store := redisstore.New(client)
	fmt.Println(store.KeyPrefix(), store.TTL())
	// Output: audit: 168h0m0s
}
