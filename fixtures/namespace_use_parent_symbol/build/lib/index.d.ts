declare interface FileReader {}
declare const FileReader: {prototype: FileReader, newReader(): FileReader, readonly EMPTY: 0, readonly LOADING: 1, readonly DONE: 2};
declare namespace web_assembly {
  interface ValueTypeMap {
    anyfunc: Function;
    externref: any;
    f32: number;
    f64: number;
    i32: number;
    i64: bigint;
    v128: never;
  }
  type ValueType = keyof ValueTypeMap;
  interface GlobalDescriptor<T extends ValueType = ValueType> {
    mutable?: boolean;
    value: T;
  }
}
