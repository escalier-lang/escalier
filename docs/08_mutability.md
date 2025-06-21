# 08 Mutability

Escalier is immutable by default.  This differs from TypeScript which is mutable
by default.



## Interop

TypeScript includes some official types that make the distinction between mutable
and immutable versions of the same class, e.g. `Array`/`ReadonlyArray`, 
`Set`/`ReadonlySet`, and `Map`/`ReadonlyMap`.  The majority of classes in the
TypeScript ecosystem do not make this distinction.

Escalier uses these pairs to construct an Escalier type for class instances
that marks methods as mutating or not

