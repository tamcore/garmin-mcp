//go:build windows

package securefile

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The Windows syscall layer. It reads and writes security descriptors and turns
// them into the platform-independent structs in acl.go, which hold the actual
// rule. Nothing here decides anything.

// readSecurityInformation is what a load needs: who owns the object, and who the
// DACL grants access to.
const readSecurityInformation = windows.OWNER_SECURITY_INFORMATION | windows.DACL_SECURITY_INFORMATION

// writeSecurityInformation sets owner and DACL and marks the DACL protected, which
// is what stops a permissive parent directory from re-adding entries.
const writeSecurityInformation = windows.OWNER_SECURITY_INFORMATION |
	windows.DACL_SECURITY_INFORMATION |
	windows.PROTECTED_DACL_SECURITY_INFORMATION

// currentUserSID reports the SID of the account this process runs as, in the
// rendered S-1-… form.
func currentUserSID() (string, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", fmt.Errorf("securefile: resolve the current user: %w", err)
	}
	return user.User.Sid.String(), nil
}

// readAccessControl reads the owner and the DACL of an open file through its
// handle, so the description belongs to the object that was opened rather than to
// whatever the path names now.
func readAccessControl(file *os.File) (accessControl, error) {
	descriptor, err := windows.GetSecurityInfo(windows.Handle(file.Fd()),
		windows.SE_FILE_OBJECT, readSecurityInformation)
	if err != nil {
		return accessControl{}, fmt.Errorf("securefile: read security information: %w", err)
	}
	return describeSecurityDescriptor(descriptor)
}

// describeSecurityDescriptor converts a security descriptor into the form acl.go
// evaluates.
func describeSecurityDescriptor(descriptor *windows.SECURITY_DESCRIPTOR) (accessControl, error) {
	flags, _, err := descriptor.Control()
	if err != nil {
		return accessControl{}, fmt.Errorf("securefile: read descriptor control flags: %w", err)
	}
	control := accessControl{protected: flags&windows.SE_DACL_PROTECTED != 0}

	if owner, _, ownerErr := descriptor.Owner(); ownerErr == nil && owner != nil {
		control.owner = owner.String()
	}

	dacl, _, err := descriptor.DACL()
	if err != nil {
		return accessControl{}, fmt.Errorf("securefile: read the access control list: %w", err)
	}
	if dacl == nil {
		return control, nil
	}
	control.daclPresent = true

	entries, err := describeACL(dacl)
	if err != nil {
		return accessControl{}, err
	}
	control.entries = entries
	return control, nil
}

// describeACL converts every entry of a DACL.
func describeACL(acl *windows.ACL) ([]accessEntry, error) {
	entries := make([]accessEntry, 0, acl.AceCount)
	for index := uint32(0); index < uint32(acl.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, index, &ace); err != nil {
			return nil, fmt.Errorf("securefile: read access control entry %d: %w", index, err)
		}
		entries = append(entries, describeACE(ace))
	}
	return entries, nil
}

// describeACE converts one entry. Every entry type starts with the same header and
// mask and puts a SID after the mask, so reading those three is safe whatever the
// concrete type is; an unrecognized type is reported as aceOther and refused by the
// rule rather than parsed further.
func describeACE(ace *windows.ACCESS_ALLOWED_ACE) accessEntry {
	sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	return accessEntry{
		sid:       sid.String(),
		kind:      aceKindOf(ace.Header.AceType),
		mask:      uint32(ace.Mask),
		inherited: ace.Header.AceFlags&windows.INHERITED_ACE != 0,
	}
}

func aceKindOf(aceType uint8) aceKind {
	switch aceType {
	case windows.ACCESS_ALLOWED_ACE_TYPE:
		return aceAllow
	case windows.ACCESS_DENIED_ACE_TYPE:
		return aceDeny
	}
	return aceOther
}

// applyOwnerOnlyACL makes path owner-only: the current user owns it, the DACL is
// protected from inheritance, and its single entry grants that user full control.
// It is the Windows equivalent of chmod 0600, and it is exactly the shape
// checkOwnerOnlyAccess accepts, so what a write installs is what a load requires.
//
// The descriptor is set by name rather than through the open handle because
// changing a DACL needs WRITE_DAC and WRITE_OWNER access, which os.OpenFile cannot
// request. The name was resolved through a verified directory descriptor
// immediately before.
func applyOwnerOnlyACL(path string) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("securefile: resolve the current user for %q: %w", path, err)
	}
	owner := user.User.Sid

	dacl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(owner),
		},
	}}, nil)
	if err != nil {
		return fmt.Errorf("securefile: build the access control list for %q: %w", path, err)
	}

	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		writeSecurityInformation, owner, nil, dacl, nil); err != nil {
		return fmt.Errorf("securefile: restrict %q to its owner: %w", path, err)
	}
	return nil
}
