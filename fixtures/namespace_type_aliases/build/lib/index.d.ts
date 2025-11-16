declare namespace bar {
  type Bar = string;
  const foo: foo.Foo;
}
declare namespace foo {
  type Foo = number;
  const bar: bar.Bar;
}
