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
export const nsDerived = new geo__NsDerived();
export const nsTag = nsDerived.tag;
export const nsWho = nsDerived.who();
class util__QualifiedDerived extends geo__Base {
  constructor() {
    super();
  }
}
export const qualifiedDerived = new util__QualifiedDerived();
export const qualifiedWho = qualifiedDerived.who();
class util__UtilDerived extends Base {
  constructor() {
    super();
  }
}
export const utilDerived = new util__UtilDerived();
export const utilTag = utilDerived.tag;
export const utilWho = utilDerived.who();
//# sourceMappingURL=./index.js.map
