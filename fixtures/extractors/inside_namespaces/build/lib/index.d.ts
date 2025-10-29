declare type MyEnum = MyEnum.Color | MyEnum.Event;
declare const obj: MyEnum;
declare const result1: number | string;
declare const result2: number | string;
declare namespace MyEnum {
  type Color = {r: number, g: number, b: number};
  const Color: {new (r: number, g: number, b: number): Color, [Symbol.customMatcher](subject: Color): [number, number, number]};
  type Event = {kind: string};
  const Event: {new (kind: string): Event, [Symbol.customMatcher](subject: Event): [string]};
}
