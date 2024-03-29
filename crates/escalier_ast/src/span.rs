use std::cmp::{max, min};
use std::fmt;

#[derive(Clone, Copy, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub struct Span {
    pub start: usize,
    pub end: usize,
}

impl fmt::Debug for Span {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{}..{}", self.start, self.end)
    }
}

pub const DUMMY_SPAN: Span = Span { start: 0, end: 0 };

pub fn merge_spans(left: &Span, right: &Span) -> Span {
    Span {
        start: min(left.start, right.start),
        end: max(left.end, right.end),
    }
}
