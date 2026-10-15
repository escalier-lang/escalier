declare type Base = {tag: string, who(): string};
declare const Base: {new (): Base};
declare const nsDerived: geo.NsDerived;
declare const nsTag: string;
declare const nsWho: string;
declare const qualifiedDerived: util.QualifiedDerived;
declare const qualifiedWho: string;
declare const utilDerived: util.UtilDerived;
declare const utilTag: string;
declare const utilWho: string;
declare namespace geo {
  type Base = {tag: string, who(): string};
  const Base: {new (): Base};
  type NsDerived = {};
  const NsDerived: {new (): NsDerived};
}
declare namespace util {
  type QualifiedDerived = {};
  const QualifiedDerived: {new (): QualifiedDerived};
  type UtilDerived = {};
  const UtilDerived: {new (): UtilDerived};
}
