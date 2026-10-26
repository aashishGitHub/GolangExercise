package twoway

type Field string

const (
	FieldTitle  Field = "title"
	FieldStatus Field = "status"
	FieldNotes  Field = "notes"
)

type Owner string

const (
	OwnerInternal Owner = "internal"
	OwnerExternal Owner = "external"
)

// Ownership says which system is the source of truth per field. A field not
// listed is internal-owned, so a forgotten entry can never let the external
// system overwrite local data.
type Ownership map[Field]Owner

func DefaultOwnership() Ownership {
	return Ownership{FieldTitle: OwnerExternal, FieldStatus: OwnerExternal, FieldNotes: OwnerInternal}
}

func (o Ownership) externalOwns(f Field) bool {
	return o[f] == OwnerExternal
}

// Merge takes external-owned fields from incoming and keeps internal-owned
// fields from current. rejected lists internal-owned fields where incoming
// disagreed: the external system edited a field it does not own.
func (o Ownership) Merge(current, incoming Content) (merged Content, rejected []Field) {
	pick := func(f Field, cur, in string) string {
		if o.externalOwns(f) {
			return in
		}
		if in != cur {
			rejected = append(rejected, f)
		}
		return cur
	}
	merged = Content{
		Title:  pick(FieldTitle, current.Title, incoming.Title),
		Status: pick(FieldStatus, current.Status, incoming.Status),
		Notes:  pick(FieldNotes, current.Notes, incoming.Notes),
	}
	return merged, rejected
}

// PatchFor selects the internal-owned fields: the only ones we ever write to
// the external system.
func (o Ownership) PatchFor(c Content) IssuePatch {
	var p IssuePatch
	if !o.externalOwns(FieldTitle) {
		p.Summary = &c.Title
	}
	if !o.externalOwns(FieldStatus) {
		p.Status = &c.Status
	}
	if !o.externalOwns(FieldNotes) {
		p.Description = &c.Notes
	}
	return p
}

func (p IssuePatch) empty() bool {
	return p.Summary == nil && p.Status == nil && p.Description == nil
}
