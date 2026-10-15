package utils

import (
	"encoding/json"
	"testing"
)

func TestEncryptDecryptBytes(t *testing.T) {
	secretKey := "my-super-secret-key-for-testing"
	originalJSON := []byte(`{"access_token":"EAAB12345XYZ","phone_number_id":"1092837465"}`)

	encrypted, err := EncryptBytes(originalJSON, secretKey)
	if err != nil {
		t.Fatalf("EncryptBytes failed: %v", err)
	}

	// Verify that the encrypted value is valid JSON so PostgreSQL JSONB can accept it
	var jsonVal interface{}
	if err := json.Unmarshal(encrypted, &jsonVal); err != nil {
		t.Fatalf("Encrypted bytes is not valid JSON: %v. Content: %s", err, string(encrypted))
	}

	decrypted, err := DecryptBytes(encrypted, secretKey)
	if err != nil {
		t.Fatalf("DecryptBytes failed: %v", err)
	}

	if string(decrypted) != string(originalJSON) {
		t.Fatalf("Decrypted mismatch. Expected %s, got %s", string(originalJSON), string(decrypted))
	}
}

func TestDecryptLegacyPlaintext(t *testing.T) {
	secretKey := "my-super-secret-key-for-testing"
	legacyJSON := []byte(`{"bot_token":"123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11"}`)

	decrypted, err := DecryptBytes(legacyJSON, secretKey)
	if err != nil {
		t.Fatalf("DecryptBytes failed on legacy JSON: %v", err)
	}

	if string(decrypted) != string(legacyJSON) {
		t.Fatalf("Expected untouched legacy JSON, got %s", string(decrypted))
	}
}
