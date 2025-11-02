declare namespace MyOption {
  export type Some = {};
  export const Some: {new (value: T): MyOption<T>, "[Symbol(2)]"(subject: Some): [T]};
  export type None = {};
  export const None: {new (): MyOption<T>, "[Symbol(2)]"(subject: None): []};
}
declare type MyOption<T> = MyOption.Some<T> | MyOption.None<T>;
declare const option: MyOption<number>;
declare const result: number | 0;
