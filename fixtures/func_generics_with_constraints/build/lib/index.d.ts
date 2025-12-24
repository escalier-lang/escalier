declare function addNums1(a: number, b: number): number;
declare function addStrs1<A extends string, B extends string>(a: A, b: B): number;
declare function addNums2<A extends number, B extends number>(a: A, b: B): number;
declare const addStrs3: <A extends string, B extends string>(a: A, b: B) => number;
declare const addNums4: <A extends number, B extends number>(a: A, b: B) => number;
declare function main(): ;
