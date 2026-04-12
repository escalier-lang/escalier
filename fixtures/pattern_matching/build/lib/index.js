import { InvokeCustomMatcherOrThrow } from "@escalier/runtime";
export class Circle {
  constructor(temp1) {
    const radius = temp1;
  }
  get area() {
    return 3.14159 * radius * radius;
  }
}
export class Color {
  constructor(temp3, temp4, temp5) {
    const r = temp3;
    const g = temp4;
    const b = temp5;
    this.r = r;
    this.g = g;
    this.b = b;
  }
  static [Symbol.customMatcher](temp2) {
    const subject = temp2;
    return [subject.r, subject.g, subject.b];
  }
}
export class Event {
  constructor(temp7) {
    const kind = temp7;
    this.kind = kind;
  }
  static [Symbol.customMatcher](temp6) {
    const subject = temp6;
    return [subject.kind];
  }
}
export class User {
  constructor(temp8, temp9, temp10) {
    const name = temp8;
    const age = temp9;
    const email = temp10;
    this.name = name;
    this.age = age;
    this.email = email;
  }
}
export const bool = true;
let temp11;
let temp12;
temp12 = bool;
if (temp12 == true) {
  temp11 = "yes";
} else if (temp12 == false) {
  temp11 = "no";
}
export const boolMatch = temp11;
export const extendedPoint = {x: 1, y: 2, z: 3, name: "point"};
let temp13;
let temp14;
temp14 = circle;
if (temp14 != null && "area" in temp14) {
  const {area} = temp14;
  temp13 = area;
}
export const getterMatch = temp13;
export const tuple2 = [3, 3];
let temp15;
let temp16;
temp16 = tuple2;
if (temp16.length == 2) {
  const [a, b] = temp16;
  if (a == b) {
    temp15 = "equal";
  } else if (temp16.length == 2) {
    const [a, b] = temp16;
    temp15 = "not equal";
  }
}
export const guardMatch = temp15;
export const num1 = 5;
let temp17;
let temp18;
temp18 = num1;
if (temp18 == 1) {
  temp17 = "one";
} else if (temp18 == 2) {
  temp17 = "two";
} else if (temp18 == 5) {
  temp17 = "five";
} else {
  temp17 = "other";
}
export const literalMatch = temp17;
let temp19;
let temp20;
temp20 = point;
if (temp20 != null && "x" in temp20 && temp20.x == 0 && "y" in temp20 && temp20.y == 0) {
  const {x: _, y: _} = temp20;
  temp19 = "origin";
} else if (temp20 != null && "x" in temp20 && "y" in temp20) {
  const {x, y} = temp20;
  temp19 = "other";
}
export const literalPatternMatch = temp19;
export const longTuple = [1, 2, 3];
export const num3 = 7;
let temp21;
let temp22;
temp22 = num3;
if (temp22 == 1) {
  temp21 = "one";
} else if (temp22 == 2) {
  temp21 = "two";
} else {
  const n = temp22;
  temp21 = "number: " + n.toString();
}
export const mixedMatch = temp21;
let temp23;
let temp24;
temp24 = obj3;
if (temp24 instanceof Color && temp24 != null && "r" in temp24 && "g" in temp24 && "b" in temp24) {
  const {r, g, b} = temp24;
  temp23 = r + g + b;
} else if (temp24 != null && "kind" in temp24) {
  const {kind} = temp24;
  temp23 = kind;
}
export const mixedNominalStructural = temp23;
export const str = "hello";
let temp25;
let temp26;
temp26 = str;
if (temp26 == "hi") {
  temp25 = "greeting";
} else if (temp26 == "hello") {
  temp25 = "salutation";
} else if (temp26 == "bye") {
  temp25 = "farewell";
} else {
  temp25 = "unknown";
}
export const multiCase = temp25;
export const nestedValue = {point: [1, 2]};
let temp27;
let temp28;
temp28 = nestedValue;
if (temp28 != null && "point" in temp28 && temp28.point.length == 2) {
  const {point: [x, y]} = temp28;
  temp27 = x + y;
} else {
  temp27 = 0;
}
export const nestedMatch = temp27;
export let num2 = 42;
export const objectValue = {x: 10, y: 20};
let temp29;
let temp30;
temp30 = objectValue;
if (temp30 != null && "x" in temp30 && "y" in temp30) {
  const {x, y = 0} = temp30;
  temp29 = x + y;
} else {
  temp29 = 0;
}
export const objectMatch = temp29;
let temp31;
let temp32;
temp32 = objectValue;
if (temp32 != null && "x" in temp32 && "y" in temp32) {
  const {x: a, y: b = 0} = temp32;
  temp31 = a * b;
} else {
  temp31 = 0;
}
export const objectRename = temp31;
let temp33;
let temp34;
temp34 = extendedPoint;
if (temp34 != null && "x" in temp34 && "y" in temp34) {
  const {x, y, ...rest} = temp34;
  temp33 = rest;
} else {
  temp33 = 0;
}
export const objectRestMatch = temp33;
let temp35;
let temp36;
temp36 = user;
if (temp36 != null && "name" in temp36) {
  const {name} = temp36;
  temp35 = name;
}
export const partialMatch = temp35;
let temp37;
let temp38;
temp38 = ref;
if (temp38 != null && "value" in temp38 && typeof temp38.value == "string") {
  const {value: a} = temp38;
  temp37 = "string";
} else if (temp38 != null && "value" in temp38 && typeof temp38.value == "number") {
  const {value: b} = temp38;
  temp37 = "number";
} else if (temp38 != null && "value" in temp38 && typeof temp38.value == "boolean") {
  const {value: c} = temp38;
  temp37 = "boolean";
}
export const refMatch = temp37;
let temp39;
let temp40;
temp40 = obj;
if (temp40 instanceof Color && temp40 != null && "r" in temp40 && "g" in temp40 && "b" in temp40) {
  const {r, g = 0, b: blue = 0} = temp40;
  temp39 = r + g + blue;
} else if (temp40 instanceof Event && temp40 != null && "kind" in temp40) {
  const {kind = "default"} = temp40;
  temp39 = kind;
}
export const result1 = temp39;
let temp41;
let temp42;
temp42 = obj;
if (temp42 instanceof Color) {
  const [temp44, temp45, temp46 = 0] = InvokeCustomMatcherOrThrow(Color, temp42, undefined);
  const r = temp44;
  const g = temp45;
  const blue = temp46;
  temp41 = r + g + blue;
} else if (temp42 instanceof Event) {
  const [temp43 = "default"] = InvokeCustomMatcherOrThrow(Event, temp42, undefined);
  const kind = temp43;
  temp41 = kind;
}
export const result2 = temp41;
let temp47;
let temp48;
temp48 = shape;
if (temp48 != null && "kind" in temp48) {
  const {kind} = temp48;
  temp47 = kind;
}
export const sharedFieldSameType = temp47;
let temp49;
let temp50;
temp50 = fbb;
if (temp50 != null && "value" in temp50) {
  const {value} = temp50;
  temp49 = value;
}
export const sharedFieldsMatch = temp49;
let temp51;
let temp52;
temp52 = obj2;
if (temp52 != null && "r" in temp52 && "g" in temp52 && "b" in temp52) {
  const {r, g, b} = temp52;
  temp51 = r + g + b;
} else if (temp52 != null && "kind" in temp52) {
  const {kind} = temp52;
  temp51 = kind;
}
export const structuralUnionMatch = temp51;
export const tupleValue = [1, 2, 3];
let temp53;
let temp54;
temp54 = tupleValue;
if (temp54.length == 3) {
  const [a, b, c] = temp54;
  temp53 = a + b + c;
} else {
  temp53 = 0;
}
export const tupleMatch = temp53;
let temp55;
let temp56;
temp56 = longTuple;
if (temp56.length == 2) {
  const [first = 0, ...rest] = temp56;
  temp55 = rest;
} else {
  temp55 = 0;
}
export const tupleRestMatch = temp55;
let temp57;
let temp58;
temp58 = num2;
if (true) {
  const x = temp58;
  temp57 = x * 2;
}
export const variableMatch = temp57;
//# sourceMappingURL=./index.js.map
