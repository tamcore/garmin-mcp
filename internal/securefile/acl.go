package securefile

import (
	"fmt"
	"strings"
)

// Windows access control, expressed without any Windows type.
//
// This file is the decision: given an owner, a DACL and the identity of the current
// process, is the object owner-only? It is deliberately free of syscalls and of
// build tags, so the rule is unit-testable on every platform. acl_windows.go is the
// only place that talks to Windows, and it does nothing but fill these structs in
// and hand them over.
//
// The rule is strict on purpose. A key file or a token file is created with a
// protected DACL holding exactly one entry, so anything else means the object was
// planted, inherited from a permissive parent, or widened after the fact.

// aceKind is the part of an access control entry type this package acts on.
type aceKind int

const (
	// aceAllow grants the access in the mask.
	aceAllow aceKind = iota
	// aceDeny withholds it, which can never widen access.
	aceDeny
	// aceOther is any other entry type: an audit entry, a callback entry or an
	// object entry. None of those belongs on a file this package writes.
	aceOther
)

// accessEntry is one access control entry.
type accessEntry struct {
	// sid is the principal, rendered in the S-1-… form.
	sid string
	// kind says whether the entry grants, withholds or is something else.
	kind aceKind
	// mask is the access mask, reported in errors for diagnosis.
	mask uint32
	// inherited reports the INHERITED_ACE flag: the entry came from a parent
	// directory rather than from this package.
	inherited bool
}

// accessControl is the security information of one object.
type accessControl struct {
	// owner is the owner SID.
	owner string
	// daclPresent is false for a NULL DACL, which grants everyone full control.
	daclPresent bool
	// protected reports SE_DACL_PROTECTED: the DACL does not inherit entries from
	// its parent directory.
	protected bool
	// entries are the DACL's entries in order.
	entries []accessEntry
}

// checkOwnerOnlyAccess reports whether control grants access to self alone.
//
// self is the current process's user SID. It refuses, in this order: an unknown
// self, a NULL DACL, a foreign or unknown owner, a DACL that still inherits, and
// any entry that is inherited, of an unrecognized type, or that allows a principal
// other than self. An empty DACL is accepted: it grants nothing.
func checkOwnerOnlyAccess(control accessControl, self, path string) error {
	refuse := func(reason string) error {
		return fmt.Errorf("securefile: %q %s: %w", path, reason, ErrInsecurePermissions)
	}

	switch {
	case self == "":
		return refuse("cannot be checked because the current user is unknown")
	case !control.daclPresent:
		return refuse("has no access control list, which grants every account")
	case control.owner == "":
		return refuse("has no readable owner")
	case !sameSID(control.owner, self):
		return refuse("is owned by " + control.owner + ", not by the current user")
	case !control.protected:
		return refuse("still inherits access control entries from its parent")
	}

	for index, entry := range control.entries {
		if err := checkEntry(entry, self); err != nil {
			return fmt.Errorf("securefile: %q entry %d %w", path, index, err)
		}
	}
	return nil
}

// checkEntry applies the per-entry rule. The returned error is a fragment: the
// caller prefixes it with the object and the entry index.
func checkEntry(entry accessEntry, self string) error {
	if entry.inherited {
		return fmt.Errorf("is inherited from a parent directory: %w", ErrInsecurePermissions)
	}
	if entry.kind == aceDeny {
		return nil
	}
	if entry.kind != aceAllow {
		return fmt.Errorf("has an unrecognized type: %w", ErrInsecurePermissions)
	}

	switch {
	case entry.sid == "":
		return fmt.Errorf("allows an unreadable principal: %w", ErrInsecurePermissions)
	case !sameSID(entry.sid, self):
		return fmt.Errorf("allows %s access 0x%X: %w", entry.sid, entry.mask, ErrInsecurePermissions)
	}
	return nil
}

// sameSID compares two rendered SIDs. Windows renders them in upper case but
// compares them case-insensitively, so the comparison folds case rather than
// trusting the renderer.
func sameSID(left, right string) bool {
	return strings.EqualFold(strings.TrimSpace(left), strings.TrimSpace(right))
}
