package persistence

import "go.mongodb.org/mongo-driver/v2/bson"

// MatchSnapshotValidator is the strict MongoDB collection validator for the
// current authoritative snapshot envelope.
func MatchSnapshotValidator() bson.M {
	return bson.M{
		"$jsonSchema": bson.M{
			"bsonType": "object",
			"required": []string{"_id", "schema_version", "match_version", "origin", "configuration_hash", "state", "created_at", "updated_at"},
			"properties": bson.M{
				"_id":                bson.M{"bsonType": "objectId"},
				"schema_version":     bson.M{"bsonType": "int", "enum": []int{MatchSnapshotSchemaVersion}},
				"match_version":      bson.M{"bsonType": "long", "minimum": 0},
				"origin":             bson.M{"bsonType": "string", "enum": []string{string(SnapshotOriginNative)}},
				"configuration_hash": bson.M{"bsonType": "string", "minLength": 64, "maxLength": 64},
				"state":              bson.M{"bsonType": "object"},
				"created_at":         bson.M{"bsonType": "date"},
				"updated_at":         bson.M{"bsonType": "date"},
			},
		},
	}
}

func MatchCommandReceiptValidator() bson.M {
	return bson.M{"$jsonSchema": bson.M{
		"bsonType": "object",
		"required": []string{"_id", "match_id", "command_id", "request_hash", "command_type", "expected_version", "previous_version", "resulting_version", "actor", "state_json", "events_json", "created_at"},
		"properties": bson.M{
			"_id": bson.M{"bsonType": "objectId"}, "match_id": bson.M{"bsonType": "objectId"},
			"command_id":   bson.M{"bsonType": "string", "minLength": 36, "maxLength": 36},
			"request_hash": bson.M{"bsonType": "string", "minLength": 64, "maxLength": 64},
			"command_type": bson.M{"bsonType": "string"}, "expected_version": bson.M{"bsonType": "long", "minimum": 0},
			"previous_version": bson.M{"bsonType": "long", "minimum": 0}, "resulting_version": bson.M{"bsonType": "long", "minimum": 1},
			"actor": bson.M{"bsonType": "object"}, "state_json": bson.M{"bsonType": "binData"},
			"events_json": bson.M{"bsonType": "binData"}, "created_at": bson.M{"bsonType": "date"},
		},
	}}
}

func MatchActionLogValidator() bson.M {
	return bson.M{"$jsonSchema": bson.M{
		"bsonType": "object",
		"required": []string{"_id", "match_id", "command_id", "command_type", "previous_version", "resulting_version", "actor", "command_payload", "events", "created_at"},
		"properties": bson.M{
			"_id": bson.M{"bsonType": "objectId"}, "match_id": bson.M{"bsonType": "objectId"},
			"command_id": bson.M{"bsonType": "string", "minLength": 36, "maxLength": 36}, "command_type": bson.M{"bsonType": "string"},
			"previous_version": bson.M{"bsonType": "long", "minimum": 0}, "resulting_version": bson.M{"bsonType": "long", "minimum": 1},
			"actor": bson.M{"bsonType": "object"}, "command_payload": bson.M{"bsonType": "object"},
			"events": bson.M{"bsonType": "array", "items": bson.M{"bsonType": "object"}}, "created_at": bson.M{"bsonType": "date"},
		},
	}}
}

func MatchOutboxValidator() bson.M {
	return bson.M{"$jsonSchema": bson.M{
		"bsonType": "object",
		"required": []string{"_id", "event_id", "match_id", "sequence", "resulting_version", "type", "actor", "payload", "status", "attempts", "occurred_at", "created_at"},
		"properties": bson.M{
			"_id": bson.M{"bsonType": "objectId"}, "event_id": bson.M{"bsonType": "string", "minLength": 36, "maxLength": 36},
			"match_id": bson.M{"bsonType": "objectId"}, "sequence": bson.M{"bsonType": "long", "minimum": 1},
			"resulting_version": bson.M{"bsonType": "long", "minimum": 1}, "type": bson.M{"bsonType": "string"},
			"actor": bson.M{"bsonType": "object"}, "payload": bson.M{"bsonType": "object"},
			"status":   bson.M{"enum": []string{string(OutboxPending), string(OutboxPublished), string(OutboxFailed)}},
			"attempts": bson.M{"bsonType": []string{"int", "long"}, "minimum": 0}, "last_error": bson.M{"bsonType": "string"},
			"occurred_at": bson.M{"bsonType": "date"}, "created_at": bson.M{"bsonType": "date"}, "published_at": bson.M{"bsonType": "date"},
		},
	}}
}
