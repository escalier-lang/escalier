export declare type Box<T> = {value: T};
export declare const Box: {new <T>(value: T): Box<T>};
export declare type Functor<F extends URIS1> = {map: <A, B>(fa: Kind1<F, A>, f: (a: A) => B) => Kind1<F, B>};
export declare const boxFunctor: Functor<"Box">;
export declare function double<F extends URIS1>(functor: Functor<F>, fa: Kind1<F, number>): Kind1<F, number>;
export interface URItoKind1<A0> {Box: Box<A0>}
export type URIS1 = keyof URItoKind1<unknown>;
export type Kind1<F extends URIS1, A0> = URItoKind1<A0>[F];
