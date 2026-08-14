package securefile

import (
	"errors"
	"strings"
	"testing"
)

const (
	selfSID   = "S-1-5-21-1111111111-2222222222-3333333333-1001"
	otherSID  = "S-1-5-21-1111111111-2222222222-3333333333-1002"
	everyone  = "S-1-1-0"
	systemSID = "S-1-5-18"
)

// ownerOnly is the access control an owner-only file must carry: the current user
// owns it, inheritance is switched off, and one non-inherited allow entry names
// the current user.
func ownerOnly() accessControl {
	return accessControl{
		owner:       selfSID,
		daclPresent: true,
		protected:   true,
		entries: []accessEntry{
			{sid: selfSID, kind: aceAllow, mask: 0x1F01FF},
		},
	}
}

func TestCheckOwnerOnlyAccessAcceptsAnOwnerOnlyDescriptor(t *testing.T) {
	if err := checkOwnerOnlyAccess(ownerOnly(), selfSID, "key.json"); err != nil {
		t.Fatalf("checkOwnerOnlyAccess: %v", err)
	}
}

func TestCheckOwnerOnlyAccessAcceptsAnEmptyDACL(t *testing.T) {
	control := ownerOnly()
	control.entries = nil

	if err := checkOwnerOnlyAccess(control, selfSID, "key.json"); err != nil {
		t.Fatalf("an empty DACL grants nothing and must be accepted: %v", err)
	}
}

func TestCheckOwnerOnlyAccessAcceptsADenyEntryForAnotherPrincipal(t *testing.T) {
	control := ownerOnly()
	control.entries = append(control.entries, accessEntry{sid: everyone, kind: aceDeny, mask: 0x1F01FF})

	if err := checkOwnerOnlyAccess(control, selfSID, "key.json"); err != nil {
		t.Fatalf("a deny entry grants nothing and must be accepted: %v", err)
	}
}

// rejectedDescriptors are the hostile changes at the descriptor level: who owns the
// object, and whether it has a DACL that stops inheriting.
func rejectedDescriptors() map[string]func(accessControl) accessControl {
	return map[string]func(accessControl) accessControl{
		"a null DACL, which grants everyone": func(control accessControl) accessControl {
			control.daclPresent = false
			return control
		},
		"a foreign owner": func(control accessControl) accessControl {
			control.owner = otherSID
			return control
		},
		"an unknown owner": func(control accessControl) accessControl {
			control.owner = ""
			return control
		},
		"an unprotected DACL that still inherits": func(control accessControl) accessControl {
			control.protected = false
			return control
		},
	}
}

// rejectedEntries are the hostile changes at the entry level. Each one appends a
// single entry to an otherwise acceptable DACL, so the rule has to refuse on that
// entry alone.
func rejectedEntries() map[string]accessEntry {
	return map[string]accessEntry{
		"an allow entry for another local account": {sid: otherSID, kind: aceAllow, mask: 0x120089},
		"an allow entry for everyone":              {sid: everyone, kind: aceAllow, mask: 0x120089},
		"an allow entry for SYSTEM":                {sid: systemSID, kind: aceAllow, mask: 0x1F01FF},
		"an inherited allow entry for the current user": {
			sid: selfSID, kind: aceAllow, mask: 0x1F01FF, inherited: true,
		},
		"an inherited deny entry":                {sid: otherSID, kind: aceDeny, mask: 0x1F01FF, inherited: true},
		"an entry of an unrecognized type":       {sid: selfSID, kind: aceOther, mask: 0x1F01FF},
		"an allow entry with an empty principal": {kind: aceAllow, mask: 0x120089},
	}
}

// rejectedControls is every hostile descriptor the rule must refuse.
func rejectedControls() map[string]func(accessControl) accessControl {
	all := rejectedDescriptors()
	for name, entry := range rejectedEntries() {
		all[name] = func(control accessControl) accessControl {
			control.entries = append(control.entries, entry)
			return control
		}
	}
	return all
}

func TestCheckOwnerOnlyAccessRejects(t *testing.T) {
	for name, mutate := range rejectedControls() {
		t.Run(name, func(t *testing.T) {
			err := checkOwnerOnlyAccess(mutate(ownerOnly()), selfSID, "key.json")
			if !errors.Is(err, ErrInsecurePermissions) {
				t.Fatalf("checkOwnerOnlyAccess with %s: err = %v, want ErrInsecurePermissions", name, err)
			}
			if !strings.Contains(err.Error(), "key.json") {
				t.Fatalf("the error does not name the file: %v", err)
			}
		})
	}
}

// TestCheckOwnerOnlyAccessRefusesAnUnknownSelf covers the case where the process
// cannot resolve its own identity: an unverifiable owner must fail closed.
func TestCheckOwnerOnlyAccessRefusesAnUnknownSelf(t *testing.T) {
	if err := checkOwnerOnlyAccess(ownerOnly(), "", "key.json"); !errors.Is(err, ErrInsecurePermissions) {
		t.Fatalf("checkOwnerOnlyAccess with an unknown self: err = %v, want ErrInsecurePermissions", err)
	}
}

// TestCheckOwnerOnlyAccessNamesTheObjectItRefused keeps the diagnostic useful: an
// operator has to learn which file to fix, so the path must come from the argument
// rather than from a fixed string.
func TestCheckOwnerOnlyAccessNamesTheObjectItRefused(t *testing.T) {
	control := ownerOnly()
	control.owner = otherSID

	// The paths carry no backslash on purpose: the error quotes with %q, which would
	// escape one, and this assertion is about the path reaching the message at all.
	for _, path := range []string{"keys/key-v1.json", "tokens/record.json"} {
		err := checkOwnerOnlyAccess(control, selfSID, path)
		if !errors.Is(err, ErrInsecurePermissions) {
			t.Fatalf("checkOwnerOnlyAccess(%q): err = %v, want ErrInsecurePermissions", path, err)
		}
		if !strings.Contains(err.Error(), path) {
			t.Fatalf("the error does not name %q: %v", path, err)
		}
	}
}

// TestCheckOwnerOnlyAccessComparesSIDsCaseInsensitively reflects Windows, which
// renders a SID in upper case but compares it case-insensitively.
func TestCheckOwnerOnlyAccessComparesSIDsCaseInsensitively(t *testing.T) {
	control := ownerOnly()
	control.owner = strings.ToLower(selfSID)
	control.entries = []accessEntry{{sid: strings.ToLower(selfSID), kind: aceAllow, mask: 0x1F01FF}}

	if err := checkOwnerOnlyAccess(control, selfSID, "key.json"); err != nil {
		t.Fatalf("checkOwnerOnlyAccess with a lower-case SID: %v", err)
	}
}
