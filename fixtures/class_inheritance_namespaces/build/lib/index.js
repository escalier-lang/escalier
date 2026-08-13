export const geo = {};
export const util = {};
class geo__Base {
  constructor() {
    this.tag = "geo";
  }
  who() {
    return "geo base";
  }
}
geo.Base = geo__Base;
export class Base {
  constructor() {
    this.tag = "root";
  }
  who() {
    return "root base";
  }
}
class geo__NsDerived extends geo__Base {
  constructor() {
    super();
  }
}
geo.NsDerived = geo__NsDerived;
export const nsDerived = new geo.NsDerived();
export const nsTag = nsDerived.tag;
export const nsWho = nsDerived.who();
class util__QualifiedDerived extends geo__Base {
  constructor() {
    super();
  }
}
util.QualifiedDerived = util__QualifiedDerived;
export const qualifiedDerived = new util.QualifiedDerived();
export const qualifiedWho = qualifiedDerived.who();
class util__UtilDerived extends Base {
  constructor() {
    super();
  }
}
util.UtilDerived = util__UtilDerived;
export const utilDerived = new util.UtilDerived();
export const utilTag = utilDerived.tag;
export const utilWho = utilDerived.who();
//# sourceMappingURL=./index.js.map
