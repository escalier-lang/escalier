declare type MyEnum = MyEnum.Color | MyEnum.MyEvent;
declare const obj: MyEnum;
declare const result1: number | string;
declare const result2: number | string;
declare namespace MyEnum {
  type Color = {r: number, g: number, b: number};
  const Color: {new (r: number, g: number, b: number): Color, [Symbol.customMatcher](subject: Color): [number, number, number]};
  type MyEvent = {kind: string};
  const MyEvent: {new (kind: string): MyEvent, [Symbol.customMatcher](subject: MyEvent): [string]};
}
