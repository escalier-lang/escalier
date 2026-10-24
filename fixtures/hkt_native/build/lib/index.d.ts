declare type Box<T> = {value: T};
declare const Box: {new <T>(value: T): Box<T>};
declare type Functor<F> = {map: <A, B>(fa: unknown, f: (a: A) => B) => unknown};
declare namespace MyOption {
  export type Some<T> = {};
  export const Some: {new <T>(value: T): MyOption<T>, [Symbol.customMatcher]<T>(subject: Some<T>): [T]};
  export type None<T> = {};
  export const None: {new <T>(): MyOption<T>, [Symbol.customMatcher]<T>(subject: None<T>): []};
}
declare type MyOption<T> = MyOption.Some<T> | MyOption.None<T>;
declare type Tagged<E, A> = {tag: E, value: A};
declare const Tagged: {new <E, A>(tag: E, value: A): Tagged<E, A>};
declare const arrayFunctor: Functor<unknown>;
declare const box: Box<number>;
declare const boxFunctor: Functor<unknown>;
declare function double<F>(functor: Functor<F>, fa: unknown): unknown;
declare const xs: Array<number>;
declare const doubledArray: Array<number>;
declare const doubledBox: Box<number>;
declare const opt: MyOption<number>;
declare const optionFunctor: Functor<unknown>;
declare const doubledOpt: MyOption<number>;
declare const tagged: Tagged<string, number>;
declare const taggedFunctor: Functor<unknown>;
declare const doubledTagged: Tagged<string, number>;
