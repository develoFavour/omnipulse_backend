package utils

import (
	"context"

	"github.com/clerk/clerk-sdk-go/v2/user"
)

// ResolveClerkUserEmailAndName queries the Clerk API to retrieve the user's verified email and full name.
// Returns (email, name). If unable to resolve, returns empty strings.
func ResolveClerkUserEmailAndName(ctx context.Context, clerkUserID string) (string, string) {
	if clerkUserID == "" {
		return "", ""
	}

	usr, err := user.Get(ctx, clerkUserID)
	if err != nil || usr == nil {
		return "", ""
	}

	email := ""
	for _, em := range usr.EmailAddresses {
		if usr.PrimaryEmailAddressID != nil && em.ID == *usr.PrimaryEmailAddressID {
			email = em.EmailAddress
			break
		}
	}
	if email == "" && len(usr.EmailAddresses) > 0 {
		email = usr.EmailAddresses[0].EmailAddress
	}

	name := ""
	if usr.FirstName != nil && *usr.FirstName != "" {
		name = *usr.FirstName
	}
	if usr.LastName != nil && *usr.LastName != "" {
		if name != "" {
			name += " " + *usr.LastName
		} else {
			name = *usr.LastName
		}
	}

	return email, name
}
