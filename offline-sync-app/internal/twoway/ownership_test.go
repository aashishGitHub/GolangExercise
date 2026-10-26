package twoway

import (
	"reflect"
	"testing"
)

func TestMerge(t *testing.T) {
	current := Content{Title: "T", Status: "Open", Notes: "internal notes"}
	tests := []struct {
		name         string
		ownership    Ownership
		incoming     Content
		wantMerged   Content
		wantRejected []Field
	}{
		{
			name:         "external-owned fields are taken, internal-owned kept",
			ownership:    DefaultOwnership(),
			incoming:     Content{Title: "T2", Status: "Done", Notes: "internal notes"},
			wantMerged:   Content{Title: "T2", Status: "Done", Notes: "internal notes"},
			wantRejected: nil,
		},
		{
			name:         "external edit of an internal-owned field is rejected and reported",
			ownership:    DefaultOwnership(),
			incoming:     Content{Title: "T", Status: "Done", Notes: "edited in Jira"},
			wantMerged:   Content{Title: "T", Status: "Done", Notes: "internal notes"},
			wantRejected: []Field{FieldNotes},
		},
		{
			name:         "ownership is configuration: flip status to internal",
			ownership:    Ownership{FieldTitle: OwnerExternal, FieldStatus: OwnerInternal, FieldNotes: OwnerInternal},
			incoming:     Content{Title: "T", Status: "Done", Notes: "internal notes"},
			wantMerged:   Content{Title: "T", Status: "Open", Notes: "internal notes"},
			wantRejected: []Field{FieldStatus},
		},
		{
			name:         "a field missing from the config is internal-owned",
			ownership:    Ownership{},
			incoming:     Content{Title: "other", Status: "other", Notes: "other"},
			wantMerged:   current,
			wantRejected: []Field{FieldTitle, FieldStatus, FieldNotes},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			merged, rejected := tt.ownership.Merge(current, tt.incoming)

			if merged != tt.wantMerged {
				t.Errorf("merged = %+v, want %+v", merged, tt.wantMerged)
			}
			if !reflect.DeepEqual(rejected, tt.wantRejected) {
				t.Errorf("rejected = %v, want %v", rejected, tt.wantRejected)
			}
		})
	}
}

func TestPatchFor_OnlyInternalOwnedFields(t *testing.T) {
	patch := DefaultOwnership().PatchFor(Content{Title: "T", Status: "Open", Notes: "n"})

	if patch.Summary != nil || patch.Status != nil {
		t.Errorf("patch = %+v, want external-owned title and status left out", patch)
	}
	if patch.Description == nil || *patch.Description != "n" {
		t.Errorf("patch.Description = %v, want n", patch.Description)
	}
}

func TestPatchFor_NothingInternalOwnedIsEmpty(t *testing.T) {
	all := Ownership{FieldTitle: OwnerExternal, FieldStatus: OwnerExternal, FieldNotes: OwnerExternal}

	if !all.PatchFor(Content{}).empty() {
		t.Error("patch should be empty when the external system owns every field")
	}
}

func TestContentHash(t *testing.T) {
	base := Content{Title: "T", Status: "Open", Notes: "n"}

	if base.Hash() != (Content{Title: "T", Status: "Open", Notes: "n"}).Hash() {
		t.Error("equal content must hash equal")
	}
	for name, changed := range map[string]Content{
		"title":  {Title: "T2", Status: "Open", Notes: "n"},
		"status": {Title: "T", Status: "Done", Notes: "n"},
		"notes":  {Title: "T", Status: "Open", Notes: "n2"},
	} {
		if changed.Hash() == base.Hash() {
			t.Errorf("changing %s must change the hash", name)
		}
	}
}
