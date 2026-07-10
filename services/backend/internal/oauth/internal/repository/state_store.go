package repository

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/getnyx/influaudit/backend/internal/oauth/internal/model"
	"github.com/getnyx/influaudit/backend/internal/oauth/internal/service"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// stateKeyPrefix namespaces the oauth state entries within the shared Redis
// keyspace so they never collide with another feature's keys and can be
// reasoned about (and, if ever needed, scanned) as a group.
const stateKeyPrefix = "oauth:state:"

// stateStore is the Redis-backed service.StateStore.
type stateStore struct {
	rdb *redis.Client
}

var _ service.StateStore = (*stateStore)(nil)

// NewStateStore builds the Redis-backed StateStore over rdb. It returns the port
// interface so callers depend on the contract, not this concrete type.
func NewStateStore(rdb *redis.Client) service.StateStore {
	return &stateStore{rdb: rdb}
}

// Save persists the CSRF/PKCE state under a TTL. The TTL bounds the replay
// window: after it expires the key is gone and any callback quoting it is
// rejected as unknown.
func (s *stateStore) Save(ctx context.Context, state string, data model.StateData, ttl time.Duration) error {
	payload, err := encodeStateData(data)
	if err != nil {
		return errs.Wrap(err, errs.KindInternal, "oauth.state_encode",
			"could not encode authorization state")
	}
	if err := s.rdb.Set(ctx, stateKey(state), payload, ttl).Err(); err != nil {
		return errs.Wrap(err, errs.KindUnavailable, "oauth.state_write",
			"could not persist authorization state")
	}
	return nil
}

// Consume fetches and deletes the state in a single atomic step via GETDEL, so a
// state can be redeemed at most once.
//
// A plain GET followed by a DEL is wrong here: two callbacks replaying the same
// captured state could both run the GET before either ran the DEL, so both would
// observe the state and both would be accepted — exactly the CSRF replay the
// state parameter exists to prevent. GETDEL (Redis 6.2+) collapses read and
// delete into one server-side operation, so precisely one racing caller receives
// the value and every other sees a miss.
func (s *stateStore) Consume(ctx context.Context, state string) (model.StateData, bool, error) {
	payload, err := s.rdb.GetDel(ctx, stateKey(state)).Bytes()
	if err != nil {
		// A missing or already-consumed key is a normal rejection, not a fault.
		if errors.Is(err, redis.Nil) {
			return model.StateData{}, false, nil
		}
		return model.StateData{}, false, errs.Wrap(err, errs.KindUnavailable, "oauth.state_read",
			"could not read authorization state")
	}

	data, err := decodeStateData(payload)
	if err != nil {
		return model.StateData{}, false, errs.Wrap(err, errs.KindInternal, "oauth.state_decode",
			"could not decode authorization state")
	}
	return data, true, nil
}

// stateKey applies the namespace prefix to a raw state value, producing the
// Redis key the entry is stored under.
func stateKey(state string) string {
	return stateKeyPrefix + state
}

// encodeStateData serializes StateData to the bytes stored in Redis. It is a
// pure function so the round trip can be tested without a Redis server.
func encodeStateData(data model.StateData) ([]byte, error) {
	return json.Marshal(data)
}

// decodeStateData reverses encodeStateData.
func decodeStateData(payload []byte) (model.StateData, error) {
	var data model.StateData
	if err := json.Unmarshal(payload, &data); err != nil {
		return model.StateData{}, err
	}
	return data, nil
}
