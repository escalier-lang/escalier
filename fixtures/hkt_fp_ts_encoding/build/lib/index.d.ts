declare namespace MyOption {
  export type Some<T> = {};
  export const Some: {new <T>(value: T): MyOption<T>, [Symbol.customMatcher]<T>(subject: Some<T>): [T]};
  export type None<T> = {};
  export const None: {new <T>(): MyOption<T>, [Symbol.customMatcher]<T>(subject: None<T>): []};
}
declare type MyOption<T> = MyOption.Some<T> | MyOption.None<T>;
declare type URItoKind<A> = {Array: Array<A>, MyOption: MyOption<A>};
declare type URIS = keyof URItoKind<unknown>;
declare type Kind<F extends URIS, A> = URItoKind<A>[F];
declare type Functor<F extends URIS> = {URI: F, map: <A, B>(fa: Kind<F, A>, f: (a: A) => B) => Kind<F, B>};
declare const arrayFunctor: Functor<"Array">;
declare function double<F extends URIS>(functor: Functor<F>, fa: Kind<F, number>): Kind<F, number>;
declare const optionFunctor: Functor<"MyOption">;
declare const xs: Array<number>;
declare const viaInstance: Kind<"Array", number>;
declare const ys: Kind<"Array", number>;
declare const zs: Kind<"MyOption", number>;
