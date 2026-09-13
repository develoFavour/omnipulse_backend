package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
)

func main() {
	candidates := []string{
		filepath.Join(".", ".env"),
		filepath.Join("..", ".env"),
		filepath.Join("..", "..", ".env"),
		filepath.Join("..", "..", "..", ".env"),
	}
	for _, c := range candidates {
		if err := godotenv.Load(c); err == nil {
			break
		}
	}

	dbURL := os.Getenv("DATABASE_URL")
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatalf("DB connect failed: %v\n", err)
	}
	defer db.Close()

	// 1. Get WhatsApp credentials
	var tenantID, senderIdentity, status string
	var credsJSON []byte
	err = db.QueryRow(`SELECT tenant_id, sender_identity, status, encrypted_credentials FROM tenant_channels WHERE platform_name = 'whatsapp' AND status = 'active' LIMIT 1`).Scan(&tenantID, &senderIdentity, &status, &credsJSON)
	if err != nil {
		log.Fatalf("No active WhatsApp channel: %v\n", err)
	}

	var creds map[string]interface{}
	_ = json.Unmarshal(credsJSON, &creds)
	jidStr, _ := creds["jid"].(string)
	fmt.Printf("Testing WhatsApp dispatch with:\n  Tenant: %s\n  Identity: %s\n  JID: %s\n\n", tenantID, senderIdentity, jidStr)

	jid, err := types.ParseJID(jidStr)
	if err != nil {
		log.Fatalf("Invalid JID: %v\n", err)
	}

	// 2. Initialize whatsmeow store
	store.SetOSInfo("Chrome (Windows)", [3]uint32{128, 0, 0})
	logger := waLog.Stdout("WA-Test", "INFO", true)
	container := sqlstore.NewWithDB(db, "postgres", logger)
	ctx := context.Background()
	_ = container.Upgrade(ctx)

	dev, err := container.GetDevice(ctx, jid)
	if err != nil || dev == nil {
		log.Fatalf("Device not found: %v\n", err)
	}

	client := whatsmeow.NewClient(dev, logger)
	fmt.Println("Connecting to WhatsApp...")
	if err := client.Connect(); err != nil {
		log.Fatalf("Connect failed: %v\n", err)
	}

	fmt.Println("Waiting for connection handshake...")
	if !client.WaitForConnection(10 * time.Second) {
		log.Fatalf("Timed out waiting for WhatsApp connection!\n")
	}

	fmt.Println("✅ Successfully connected & authenticated with WhatsApp servers!")

	// 3. Query contacts to send a test message to
	var contactName, contactPhone string
	err = db.QueryRow(`SELECT first_name, routing_value FROM contacts WHERE channel = 'whatsapp' LIMIT 1`).Scan(&contactName, &contactPhone)
	if err != nil {
		log.Fatalf("No contact found: %v\n", err)
	}

	fmt.Printf("\nSending test message to %s (%s)...\n", contactName, contactPhone)
	recipientPhone := strings.TrimPrefix(strings.TrimSpace(contactPhone), "+")
	recipientJID := types.NewJID(recipientPhone, types.DefaultUserServer)

	testMsg := fmt.Sprintf("Hello %s! This is a test broadcast from OmniPulse WhatsApp Gateway 🚀 Sent at %s", contactName, time.Now().Format("15:04:05"))
	msg := &waE2E.Message{Conversation: proto.String(testMsg)}

	resp, err := client.SendMessage(ctx, recipientJID, msg)
	if err != nil {
		log.Fatalf("❌ SendMessage failed: %v\n", err)
	}

	fmt.Printf("🎉 SUCCESS! Message sent!\n  Message ID: %s\n  Timestamp: %v\n", resp.ID, resp.Timestamp)

	time.Sleep(3 * time.Second)
	client.Disconnect()
	fmt.Println("Disconnected.")
}
