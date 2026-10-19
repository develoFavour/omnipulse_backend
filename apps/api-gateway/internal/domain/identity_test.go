package domain

import "testing"

func TestNormalizeWhatsAppPhoneIdentity(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "international format", input: "+234 702 513 0492", want: "phone:2347025130492"},
		{name: "jid", input: "2347025130492:1@s.whatsapp.net", want: "phone:2347025130492"},
		{name: "empty identity", input: "business-name", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeWhatsAppPhoneIdentity(tt.input); got != tt.want {
				t.Fatalf("NormalizeWhatsAppPhoneIdentity(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
