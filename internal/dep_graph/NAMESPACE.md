# Namespaces

Packages in Escalier are composed of multiple files on disk.  Declarations from
all files form the corresponding module.

Directories within a package define namespaces.  Namespaces can be nested.  The
following directory structure contains a library named `example` which contains namespaces `Foo`, `Foo.Bar`, and `Baz`:

packages/
    example/
        lib/
            Foo/
                Bar/
                    a.esc
                    b.esc
                c.esc
            Baz/
                d.esc
            e.esc
            f.esc
        package.json

Decls from a.esc appear in the `Foo.Bar` namespace.  Decls from b.esc and c.esc
appear in the `Foo` namespace.  Decls from d.esc appear in the `Baz` namespace.
Decls from e.esc and f.esc appear in the root namespace.

Code inside a namespace can refer to symbols from a parent namespace without
fully qualifying the symbol.  If the symbol is shadowed by a local symbol with
the same name, you can access the shadowed symbol by qualifying the name.  If the
shadowed symbol is from the root, it can be accessed using the special namespace
identifier `$Root`.

Code can refer to symbols in a child namespace by qualifying the identifier using
the appropriate namespace.

A module consists of all declarations in a package including those in namespaces.

Type checking and codegen should process decls based on strongly connected 
components.  Strongly connected components can consist of symbols from multiple
namespaces within the same module.  The current rules regarding cycles between
decls should be maintained.
