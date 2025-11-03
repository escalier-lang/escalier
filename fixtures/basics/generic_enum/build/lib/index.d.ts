declare namespace MyOption {
  export type Some<T> = {};
  export const Some: {new <T>(value: T): MyOption<T>, [Symbol.customMatcher]<T>(subject: Some<T>): [T]};
  export type None<T> = {};
  export const None: {new <T>(): MyOption<T>, [Symbol.customMatcher]<T>(subject: None<T>): []};
}
declare type MyOption<T> = MyOption.Some<T> | MyOption.None<T>;
declare const option: MyOption<number>;
declare const result: number | 0;
