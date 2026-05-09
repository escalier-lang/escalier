export class Widget {
  constructor(temp1) {
    const name = temp1;
    this.name = name;
  }
  isReady() {
    return true;
  }
  toJSON() {
    return this.name;
  }
  clone() {
    return new Widget(this.name);
  }
  render() {
    this.name = "";
  }
}
export class Cache {
  constructor(temp2) {
    const items = temp2;
    this.items = items;
  }
  getValue() {
    return this.items.length;
  }
  getOrInsert(temp3, temp4) {
    const _key = temp3;
    const default_ = temp4;
    return default_;
  }
}
export class Point {
  constructor(temp5, temp6) {
    const x = temp5;
    const y = temp6;
    this.x = x;
    this.y = y;
  }
}
export function cannot_get_or_insert_on_immutable(temp7) {
  const c = temp7;
  c.getOrInsert("k", 0);
}
export function cannot_mutate_immutable_date(temp8) {
  const d = temp8;
  d.setHours(12);
}
export function cannot_push_to_immutable(temp9) {
  const xs = temp9;
  xs.push(1);
}
export function cannot_render_immutable(temp10) {
  const w = temp10;
  w.render();
}
export function cannot_sort_immutable(temp11) {
  const xs = temp11;
  xs.sort();
}
export function cannot_write_readonly_field_on_mut(temp12) {
  const p = temp12;
  p.x = 0;
  p.y = 1;
}
export function get_on_immutable_cache(temp13) {
  const c = temp13;
  return c.getValue();
}
export function mutate_mutable_date(temp14) {
  const d = temp14;
  d.setHours(12);
}
export function name_heuristics_on_immutable(temp15) {
  const w = temp15;
  const ready = w.isReady();
  const json = w.toJSON();
  const copy = w.clone();
  return copy;
}
export function read_from_immutable_array(temp16) {
  const xs = temp16;
  return xs.length;
}
export function read_immutable_date(temp17) {
  const d = temp17;
  return d.getHours();
}
export function string_methods_on_immutable() {
  const s = "hello";
  return s.toUpperCase();
}
export function to_sorted_on_immutable(temp18) {
  const xs = temp18;
  return xs.toSorted();
}
//# sourceMappingURL=./index.js.map
