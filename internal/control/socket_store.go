package control

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/najahiiii/xray-agent/internal/model"
	"github.com/najahiiii/xray-agent/internal/socketproto"
	bolt "go.etcd.io/bbolt"
)

var (
	metaBucket      = []byte("meta")
	eventsBucket    = []byte("events")
	eventIDsBucket  = []byte("event_ids")
	baselinesBucket = []byte("stats_baselines")
	commandsBucket  = []byte("completed_commands")
	sequenceKey     = []byte("sequence")
)

type socketStore struct {
	db *bolt.DB
}

func openSocketStore(path string) (*socketStore, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("socket outbox path required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create socket outbox directory: %w", err)
	}
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		return nil, fmt.Errorf("open socket outbox: %w", err)
	}
	store := &socketStore{db: db}
	if err := db.Update(func(tx *bolt.Tx) error {
		for _, name := range [][]byte{metaBucket, eventsBucket, eventIDsBucket, baselinesBucket, commandsBucket} {
			if _, err := tx.CreateBucketIfNotExists(name); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initialize socket outbox: %w", err)
	}
	return store, nil
}

func (s *socketStore) EnqueueCommandAck(commandID string, ack *model.AgentCommandAck) (*socketproto.Envelope, error) {
	if strings.TrimSpace(commandID) == "" || ack == nil {
		return nil, fmt.Errorf("command id and ack required")
	}
	payload := socketproto.CommandAck{CommandID: commandID, Ack: *ack}
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	rawAck, err := json.Marshal(ack)
	if err != nil {
		return nil, err
	}

	var envelope *socketproto.Envelope
	err = s.db.Update(func(tx *bolt.Tx) error {
		if err := tx.Bucket(commandsBucket).Put([]byte(commandID), rawAck); err != nil {
			return err
		}
		var enqueueErr error
		envelope, enqueueErr = enqueueEnvelope(tx, socketproto.TypeCommandAck, rawPayload)
		return enqueueErr
	})
	return envelope, err
}

func (s *socketStore) CompletedCommandAck(commandID string) (*model.AgentCommandAck, error) {
	var ack *model.AgentCommandAck
	err := s.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket(commandsBucket).Get([]byte(commandID))
		if raw == nil {
			return nil
		}
		var decoded model.AgentCommandAck
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return err
		}
		ack = &decoded
		return nil
	})
	return ack, err
}

func (s *socketStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *socketStore) Enqueue(messageType string, payload any) (*socketproto.Envelope, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	var envelope *socketproto.Envelope
	err = s.db.Update(func(tx *bolt.Tx) error {
		var enqueueErr error
		envelope, enqueueErr = enqueueEnvelope(tx, messageType, raw)
		return enqueueErr
	})
	return envelope, err
}

// EnqueueLatest coalesces snapshot-like messages while disconnected. Stats and
// command acknowledgments intentionally use Enqueue instead because every item
// must be delivered.
func (s *socketStore) EnqueueLatest(messageType string, payload any) (*socketproto.Envelope, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	var envelope *socketproto.Envelope
	err = s.db.Update(func(tx *bolt.Tx) error {
		if err := deleteEventsOfType(tx, messageType); err != nil {
			return err
		}
		var enqueueErr error
		envelope, enqueueErr = enqueueEnvelope(tx, messageType, raw)
		return enqueueErr
	})
	return envelope, err
}

// EnqueueStatsSample atomically advances persisted cumulative counter
// baselines and appends the derived delta batch to the durable outbox.
func (s *socketStore) EnqueueStatsSample(serverTime time.Time, current map[string][2]int64) (*socketproto.Envelope, error) {
	if len(current) == 0 {
		return nil, nil
	}

	canonical := make(map[string][2]int64, len(current))
	for email, counters := range current {
		key := strings.ToLower(strings.TrimSpace(email))
		if key == "" {
			continue
		}
		canonical[key] = counters
	}
	emails := make([]string, 0, len(canonical))
	for email := range canonical {
		emails = append(emails, email)
	}
	slices.Sort(emails)

	var envelope *socketproto.Envelope
	err := s.db.Update(func(tx *bolt.Tx) error {
		baselines := tx.Bucket(baselinesBucket)
		users := make([]model.UserUsage, 0, len(emails))
		for _, email := range emails {
			counters := canonical[email]
			currentUp := max(counters[0], 0)
			currentDown := max(counters[1], 0)

			previousUp, previousDown, found := decodeBaseline(baselines.Get([]byte(email)))
			deltaUp := currentUp
			deltaDown := currentDown
			if found {
				deltaUp = counterDelta(previousUp, currentUp)
				deltaDown = counterDelta(previousDown, currentDown)
			}

			if err := baselines.Put([]byte(email), encodeBaseline(currentUp, currentDown)); err != nil {
				return err
			}
			if deltaUp != 0 || deltaDown != 0 {
				users = append(users, model.UserUsage{Email: email, Uplink: deltaUp, Downlink: deltaDown})
			}
		}

		if len(users) == 0 {
			return nil
		}
		payload, err := json.Marshal(model.StatsPush{ServerTime: serverTime, Users: users})
		if err != nil {
			return err
		}
		envelope, err = enqueueEnvelope(tx, socketproto.TypeStats, payload)
		return err
	})
	return envelope, err
}

func (s *socketStore) Pending(sent map[string]struct{}, limit int) ([]socketproto.Envelope, error) {
	if limit <= 0 {
		limit = 256
	}
	result := make([]socketproto.Envelope, 0, limit)
	err := s.db.View(func(tx *bolt.Tx) error {
		cursor := tx.Bucket(eventsBucket).Cursor()
		for _, value := cursor.First(); value != nil && len(result) < limit; _, value = cursor.Next() {
			var envelope socketproto.Envelope
			if err := json.Unmarshal(value, &envelope); err != nil {
				return err
			}
			if _, alreadySent := sent[envelope.ID]; alreadySent {
				continue
			}
			result = append(result, envelope)
		}
		return nil
	})
	return result, err
}

func (s *socketStore) Ack(messageID string) (bool, error) {
	if strings.TrimSpace(messageID) == "" {
		return false, nil
	}
	removed := false
	err := s.db.Update(func(tx *bolt.Tx) error {
		ids := tx.Bucket(eventIDsBucket)
		key := ids.Get([]byte(messageID))
		if key == nil {
			return nil
		}
		keyCopy := append([]byte(nil), key...)
		if err := tx.Bucket(eventsBucket).Delete(keyCopy); err != nil {
			return err
		}
		if err := ids.Delete([]byte(messageID)); err != nil {
			return err
		}
		removed = true
		return nil
	})
	return removed, err
}

func (s *socketStore) Count() (int, error) {
	count := 0
	err := s.db.View(func(tx *bolt.Tx) error {
		count = tx.Bucket(eventsBucket).Stats().KeyN
		return nil
	})
	return count, err
}

func enqueueEnvelope(tx *bolt.Tx, messageType string, payload []byte) (*socketproto.Envelope, error) {
	sequence, err := nextSequence(tx)
	if err != nil {
		return nil, err
	}
	id, err := newMessageID()
	if err != nil {
		return nil, err
	}
	envelope := &socketproto.Envelope{
		Version:  socketproto.Version,
		ID:       id,
		Type:     messageType,
		Sequence: sequence,
		SentAt:   time.Now().UTC(),
		Payload:  payload,
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return nil, err
	}
	key := sequenceBytes(sequence)
	if err := tx.Bucket(eventsBucket).Put(key, encoded); err != nil {
		return nil, err
	}
	if err := tx.Bucket(eventIDsBucket).Put([]byte(id), key); err != nil {
		return nil, err
	}
	return envelope, nil
}

func deleteEventsOfType(tx *bolt.Tx, messageType string) error {
	events := tx.Bucket(eventsBucket)
	ids := tx.Bucket(eventIDsBucket)
	cursor := events.Cursor()
	for key, value := cursor.First(); key != nil; key, value = cursor.Next() {
		var envelope socketproto.Envelope
		if err := json.Unmarshal(value, &envelope); err != nil {
			return err
		}
		if envelope.Type != messageType {
			continue
		}
		if err := ids.Delete([]byte(envelope.ID)); err != nil {
			return err
		}
		if err := cursor.Delete(); err != nil {
			return err
		}
	}
	return nil
}

func nextSequence(tx *bolt.Tx) (uint64, error) {
	bucket := tx.Bucket(metaBucket)
	current := uint64(0)
	if raw := bucket.Get(sequenceKey); len(raw) == 8 {
		current = binary.BigEndian.Uint64(raw)
	}
	current++
	if err := bucket.Put(sequenceKey, sequenceBytes(current)); err != nil {
		return 0, err
	}
	return current, nil
}

func sequenceBytes(sequence uint64) []byte {
	value := make([]byte, 8)
	binary.BigEndian.PutUint64(value, sequence)
	return value
}

func newMessageID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func encodeBaseline(up, down int64) []byte {
	value := make([]byte, 16)
	binary.BigEndian.PutUint64(value[:8], uint64(up))
	binary.BigEndian.PutUint64(value[8:], uint64(down))
	return value
}

func decodeBaseline(value []byte) (int64, int64, bool) {
	if len(value) != 16 {
		return 0, 0, false
	}
	return int64(binary.BigEndian.Uint64(value[:8])), int64(binary.BigEndian.Uint64(value[8:])), true
}

func counterDelta(previous, current int64) int64 {
	if current <= 0 {
		return 0
	}
	if current >= previous {
		return current - previous
	}
	return current
}
