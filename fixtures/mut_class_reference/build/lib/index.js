export class Consumer {
  constructor(temp1) {
    const log = temp1;
    this.log = log;
  }
  accept(temp2) {
    const x = temp2;
    return this.log;
  }
}
export class Counter {
  constructor(temp3) {
    const count = temp3;
    this.count = count;
  }
}
export class Frozen {
  constructor(temp4) {
    const value = temp4;
    this.value = value;
  }
}
export function bump(temp5) {
  const c = temp5;
  c.count = c.count + 1;
}
export function feedNumber(temp6) {
  const c = temp6;
  return c.accept(1);
}
export function feedEither(temp7) {
  const c = temp7;
  return feedNumber(c);
}
export function readEither(temp8) {
  const f = temp8;
  return f.value;
}
export function readNumber(temp9) {
  const f = temp9;
  return readEither(f);
}
//# sourceMappingURL=./index.js.map
