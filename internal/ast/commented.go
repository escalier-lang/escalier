package ast

// Commented is implemented by every node. It holds the comments a printer
// writes around the node, so a caller that renders one subtree does not have
// to carry a separate comment map alongside it.
//
// A comment reaches exactly one slot on exactly one node. AttachComments
// decides which, and the three slots name the three positions a comment can
// take relative to the node that owns it.
type Commented interface {
	// LeadingComments returns the comments written above the node, in source
	// order.
	LeadingComments() []*Comment
	// TrailingComments returns the comments written after the node on the
	// line the node ends on, in source order.
	TrailingComments() []*Comment
	// DanglingComments returns the comments written inside the node with no
	// child to bind to, in source order. The sole comment in an empty block
	// is the case this exists for.
	DanglingComments() []*Comment

	SetLeadingComments([]*Comment)
	SetTrailingComments([]*Comment)
	SetDanglingComments([]*Comment)
}

// commentSlots is embedded in every node struct to satisfy Commented. The
// three fields start nil, so a node nothing attached to costs one nil slice
// header per slot and prints as a zero struct in a snapshot.
type commentSlots struct {
	leading  []*Comment
	trailing []*Comment
	dangling []*Comment
}

func (c *commentSlots) LeadingComments() []*Comment  { return c.leading }
func (c *commentSlots) TrailingComments() []*Comment { return c.trailing }
func (c *commentSlots) DanglingComments() []*Comment { return c.dangling }

func (c *commentSlots) SetLeadingComments(comments []*Comment)  { c.leading = comments }
func (c *commentSlots) SetTrailingComments(comments []*Comment) { c.trailing = comments }
func (c *commentSlots) SetDanglingComments(comments []*Comment) { c.dangling = comments }
