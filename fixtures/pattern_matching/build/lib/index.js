import { InvokeCustomMatcherOrThrow } from "@escalier/runtime";
export class Color {
  constructor(temp2, temp3, temp4) {
    const r = temp2;
    const g = temp3;
    const b = temp4;
    this.r = r;
    this.g = g;
    this.b = b;
  }
  static [Symbol.customMatcher](temp1) {
    const subject = temp1;
    return [subject.r, subject.g, subject.b];
  }
}
export class Event {
  constructor(temp6) {
    const kind = temp6;
    this.kind = kind;
  }
  static [Symbol.customMatcher](temp5) {
    const subject = temp5;
    return [subject.kind];
  }
}
export const bool = true;
let temp7;
let temp8;
temp8 = bool;
if (temp8 == true) {
  temp7 = "yes";
} else if (temp8 == false) {
  temp7 = "no";
}
export const boolMatch = temp7;
export const extendedPoint = {x: 1, y: 2, z: 3, name: "point"};
export const tuple2 = [3, 3];
let temp9;
let temp10;
temp10 = tuple2;
if (temp10.length == 2) {
  const [a, b] = temp10;
  if (a == b) {
    temp9 = "equal";
  } else if (temp10.length == 2) {
    const [a, b] = temp10;
    temp9 = "not equal";
  }
}
export const guardMatch = temp9;
export const num1 = 5;
let temp11;
let temp12;
temp12 = num1;
if (temp12 == 1) {
  temp11 = "one";
} else if (temp12 == 2) {
  temp11 = "two";
} else if (temp12 == 5) {
  temp11 = "five";
} else {
  temp11 = "other";
}
export const literalMatch = temp11;
export const longTuple = [1, 2, 3];
export const num3 = 7;
let temp13;
let temp14;
temp14 = num3;
if (temp14 == 1) {
  temp13 = "one";
} else if (temp14 == 2) {
  temp13 = "two";
} else {
  const n = temp14;
  temp13 = "number: " + n.toString();
}
export const mixedMatch = temp13;
export const str = "hello";
let temp15;
let temp16;
temp16 = str;
if (temp16 == "hi") {
  temp15 = "greeting";
} else if (temp16 == "hello") {
  temp15 = "salutation";
} else if (temp16 == "bye") {
  temp15 = "farewell";
} else {
  temp15 = "unknown";
}
export const multiCase = temp15;
export const nestedValue = {point: [1, 2]};
let temp17;
let temp18;
temp18 = nestedValue;
if (temp18 != null && "point" in temp18 && temp18.point.length == 2) {
  const {point: [x, y]} = temp18;
  temp17 = x + y;
} else {
  temp17 = 0;
}
export const nestedMatch = temp17;
export let num2 = 42;
export const objectValue = {x: 10, y: 20};
let temp19;
let temp20;
temp20 = objectValue;
if (temp20 != null && "x" in temp20 && "y" in temp20) {
  const {x, y = 0} = temp20;
  temp19 = x + y;
} else {
  temp19 = 0;
}
export const objectMatch = temp19;
let temp21;
let temp22;
temp22 = objectValue;
if (temp22 != null && "x" in temp22 && "y" in temp22) {
  const {x: a, y: b = 0} = temp22;
  temp21 = a * b;
} else {
  temp21 = 0;
}
export const objectRename = temp21;
let temp23;
let temp24;
temp24 = extendedPoint;
if (temp24 != null && "x" in temp24 && "y" in temp24) {
  const {x, y, ...rest} = temp24;
  temp23 = rest;
} else {
  temp23 = 0;
}
export const objectRestMatch = temp23;
let temp25;
let temp26;
temp26 = ref;
if (temp26 != null && "value" in temp26 && typeof temp26.value == "string") {
  const {value: a} = temp26;
  temp25 = "string";
} else if (temp26 != null && "value" in temp26 && typeof temp26.value == "number") {
  const {value: b} = temp26;
  temp25 = "number";
} else if (temp26 != null && "value" in temp26 && typeof temp26.value == "boolean") {
  const {value: c} = temp26;
  temp25 = "boolean";
}
export const refMatch = temp25;
let temp27;
let temp28;
temp28 = obj;
if (temp28 instanceof Color && temp28 != null && "r" in temp28 && "g" in temp28 && "b" in temp28) {
  const {r, g = 0, b: blue = 0} = temp28;
  temp27 = r + g + blue;
} else if (temp28 instanceof Event && temp28 != null && "kind" in temp28) {
  const {kind = "default"} = temp28;
  temp27 = kind;
}
export const result1 = temp27;
let temp29;
let temp30;
temp30 = obj;
if (temp30 instanceof Color) {
  const [temp32, temp33, temp34 = 0] = InvokeCustomMatcherOrThrow(Color, temp30, undefined);
  const r = temp32;
  const g = temp33;
  const blue = temp34;
  temp29 = r + g + blue;
} else if (temp30 instanceof Event) {
  const [temp31 = "default"] = InvokeCustomMatcherOrThrow(Event, temp30, undefined);
  const kind = temp31;
  temp29 = kind;
}
export const result2 = temp29;
export const tupleValue = [1, 2, 3];
let temp35;
let temp36;
temp36 = tupleValue;
if (temp36.length == 3) {
  const [a, b, c] = temp36;
  temp35 = a + b + c;
} else {
  temp35 = 0;
}
export const tupleMatch = temp35;
let temp37;
let temp38;
temp38 = longTuple;
if (temp38.length == 2) {
  const [first = 0, ...rest] = temp38;
  temp37 = rest;
} else {
  temp37 = 0;
}
export const tupleRestMatch = temp37;
let temp39;
let temp40;
temp40 = num2;
if (true) {
  const x = temp40;
  temp39 = x * 2;
}
export const variableMatch = temp39;
//# sourceMappingURL=./index.js.map
