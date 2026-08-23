package escalier.specextract

import esmeta.cfg.{Block, Branch, CFG, Call, Func, Node}
import esmeta.ir.*
import esmeta.ir.util.YetCollector
import esmeta.spec.{BuiltinHead, BuiltinPath}
import esmeta.ty.{AbruptT, CompT, NormalT}
import scala.collection.mutable.ListBuffer

/** Lowers `esmeta.cfg.CFG` to the `cfg.json` schema.
  *
  * The lowering pattern-matches ESMeta IR instructions onto the [[NodeJson]]
  * and [[ExprJson]] variants and copies structure. It reaches no conclusion
  * about mutability or aliasing; every such judgement belongs to the Go
  * analysis in §4 of `planning/ecma-262/implementation_plan.md`.
  *
  * Three shapes need reconstruction rather than a direct copy, because the IR
  * compiler has already lowered them away. Each is described where it is
  * handled: the completion guards in [[guardOf]], the argument prologue in
  * [[isPrologue]], and the `Throw` step in [[throwNode]].
  */
final class Lowering(cfg: CFG):

  /** The IR spells these names as methods, which a pattern cannot refer to. */
  private val ThisName = Name(THIS_STR)
  private val ArgsName = Name(ARGS_STR)
  private val ArgsListName = Name(ARGS_LIST_STR)

  /** How ESMeta prefixes the name of every builtin algorithm. */
  private val IntrinsicPrefix = "INTRINSICS."

  /** Functions ESMeta compiles from the runtime semantics of the language, one
    * per grammar production and operation. They are the largest category in the
    * graph and none of them annotates a library surface, so they are dropped.
    */
  private def isSyntaxDirected(func: Func): Boolean =
    func.irFunc.kind == esmeta.ir.FuncKind.SynDirOp

  /** How many builtin algorithms the graph holds. The lowering keys a builtin
    * off its algorithm head rather than off this kind, so comparing the two
    * counts catches a builtin that fell through to the abstract-operation
    * branch and lost its canonical name.
    */
  def builtinCount: Int =
    cfg.funcs.count(_.irFunc.kind == esmeta.ir.FuncKind.Builtin)

  def result: CfgJson =
    val target = cfg.spec.version
      .map(_.hash)
      .getOrElse(
        throw new IllegalStateException(
          "the extracted specification carries no git version; " +
          "check that tools/spec-extract/esmeta/ecma262 is a git checkout",
        ),
      )
    val lowered = cfg.funcs.iterator
      .filterNot(isSyntaxDirected)
      .map(func => func -> lowerFunc(func))
      .toVector
      .sortBy((_, json) => (json.kind, json.name))
    checkUniqueNames(lowered)
    CfgJson(target, lowered.map((_, json) => json))

  /** A callee name resolves against the `abstract-op` functions, so those names
    * must be unique among themselves. Builtin keys live in a separate space,
    * and the two overlap on purpose: the `Set` abstract operation and the `Set`
    * constructor are different functions under the same name.
    */
  private def checkUniqueNames(lowered: Vector[(Func, FuncJson)]): Unit =
    val duplicates = lowered
      .groupBy((_, json) => (json.kind == FuncKinds.AbstractOp, json.name))
      .collect { case (_, group) if group.length > 1 => group }
    duplicates.minByOption(group => group.head._2.name).foreach { group =>
      val (_, first) = group.head
      val sources = group.map((func, _) => func.irFunc.name).mkString(", ")
      throw new IllegalStateException(
        s"${group.length} functions share the name '${first.name}' within " +
        s"the ${first.kind} name space: $sources",
      )
    }

  private def lowerFunc(func: Func): FuncJson =
    val irFunc = func.irFunc
    // A closure defined inside a builtin algorithm carries that algorithm's
    // head, so the kind rather than the head decides which name space a
    // function belongs to. The closure keeps its ESMeta name, which is what a
    // call to it names.
    val builtinHead = irFunc.kind match
      case esmeta.ir.FuncKind.Builtin =>
        irFunc.head.collect { case head: BuiltinHead => head }
      case _ => None
    val (name, kind, params) = builtinHead match
      case Some(head) =>
        (
          canonicalKey(head.path),
          builtinKind(head.path),
          head.params.map(_.name),
        )
      case None if irFunc.kind == esmeta.ir.FuncKind.Builtin =>
        // ESMeta supplies a handful of builtins from its own manual sources
        // rather than from spec.html, so they arrive without an algorithm head.
        // They are still part of the surface the analysis keys, so the name and
        // the parameters are read off the compiled function instead. Most of
        // these have a `yet` body, which the analysis reads as unclassified.
        val key = irFunc.name.stripPrefix(IntrinsicPrefix)
        val manualKind =
          if (key.contains(".prototype.") || key.endsWith(".prototype"))
            FuncKinds.BuiltinMethod
          else FuncKinds.BuiltinStatic
        (key, manualKind, poppedFormals(func))
      case None =>
        (irFunc.name, FuncKinds.AbstractOp, irFunc.params.map(_.lhs.name))
    val ctx = FuncCtx(func, kind != FuncKinds.AbstractOp, params.toSet)
    FuncJson(name, kind, params, isPromise(ctx), lowerNodes(ctx))

  // ///////////////////////////////////////////////////////////////////////////
  // Names
  // ///////////////////////////////////////////////////////////////////////////

  /** Renders a builtin's path as the canonical spec key of Appendix C. ESMeta
    * spells the same path differently: `get:X.y` for an accessor and
    * `X[%Symbol.y%]` for a symbol-keyed method.
    */
  private def canonicalKey(path: BuiltinPath): String =
    import BuiltinPath.*
    path match
      case Base(name)                 => name
      case NormalAccess(base, name)   => s"${canonicalKey(base)}.$name"
      case Getter(base)               => s"get ${canonicalKey(base)}"
      case Setter(base)               => s"set ${canonicalKey(base)}"
      case SymbolAccess(base, symbol) => s"${canonicalKey(base)} [ @@$symbol ]"
      case YetPath(name)              => name

  /** A builtin has a receiver exactly when it hangs off a prototype. That
    * covers `Array.prototype.push` and `Array.prototype [ @@iterator ]` but not
    * `Array.from` or `Array [ @@species ]`.
    *
    * A prototype that has no name of its own in the language is spelled as the
    * root of the path rather than as a `prototype` segment, so
    * `ArrayIteratorPrototype.next` is a method too.
    */
  private def builtinKind(path: BuiltinPath): String =
    import BuiltinPath.*
    def onPrototype(path: BuiltinPath): Boolean = path match
      case Base(name)                   => name.endsWith("Prototype")
      case NormalAccess(_, "prototype") => true
      case NormalAccess(base, _)        => onPrototype(base)
      case Getter(base)                 => onPrototype(base)
      case Setter(base)                 => onPrototype(base)
      case SymbolAccess(base, _)        => onPrototype(base)
      case YetPath(_)                   => false
    if (onPrototype(path)) FuncKinds.BuiltinMethod else FuncKinds.BuiltinStatic

  /** The parameters a builtin without an algorithm head declares, read off the
    * argument prologue in the order it takes them out of the argument list.
    */
  private def poppedFormals(func: Func): List[String] =
    func.nodes.toList
      .sortBy(_.id)
      .flatMap {
        case block: Block =>
          block.insts.toList.collect {
            case IPop(lhs, ERef(ArgsListName), _) => localName(lhs)
          }
        case _ => Nil
      }
      .distinct

  private def localName(local: Local): String = local match
    case Name(name) => name
    case Temp(idx)  => s"%$idx"

  private def varName(v: Var): String = v match
    case Global(name) => s"@$name"
    case local: Local => localName(local)

  /** Reads the error class off the intrinsic name ESMeta constructs the error
    * object from: `"%RangeError.prototype%"` names `RangeError`.
    */
  private def errorClass(intrinsic: String): String =
    intrinsic.stripPrefix("%").stripSuffix("%").stripSuffix(".prototype")

  // ///////////////////////////////////////////////////////////////////////////
  // Per-function context
  // ///////////////////////////////////////////////////////////////////////////

  /** What lowering one function needs beyond the node being walked.
    *
    * @param isBuiltin
    *   whether ESMeta compiled this function with the builtin calling
    *   convention, where the receiver is the `this` parameter and the arguments
    *   arrive in a list
    * @param formals
    *   the declared parameter names, used to recognize the argument prologue
    */
  private final class FuncCtx(
    val func: Func,
    val isBuiltin: Boolean,
    val formals: Set[String],
  ):
    /** Every scan below walks this rather than the graph's node set, so a local
      * bound more than once resolves to the same binding on every run and the
      * committed file does not churn.
      */
    val nodes: List[Node] = func.nodes.toList.sortBy(_.id)

    private val calls: List[Call] = nodes.collect { case call: Call => call }

    /** every call's callee name and arguments, keyed by the local it binds */
    val callDefs: Map[String, (String, List[Expr])] = calls.collect {
      case Call(_, ICall(lhs, fexpr, args), _) =>
        localName(lhs) -> (calleeName(fexpr), args)
    }.toMap

    /** locals bound to a `ThrowCompletion` result */
    val throwResults: Set[String] =
      callDefs.collect { case (lhs, ("ThrowCompletion", _)) => lhs }.toSet

    /** the then-node of each abrupt-check branch, mapped to the local the
      * branch tests, so the completion forward on that edge can be dropped
      */
    val abruptThens: Map[Int, String] = nodes.flatMap {
      case branch: Branch if branch.isAbruptNode =>
        branch.cond match
          case ETypeCheck(ERef(local: Local), ty) if ty.ty == AbruptT =>
            branch.thenNode.map(_.id -> localName(local))
          case _ => None
      case _ => None
    }.toMap

    /** the reconstructed completion guard of each call node */
    val guards: Map[Int, String] =
      calls.map(call => call.id -> guardOf(call)).toMap

    private val blockInsts: List[NormalInst] = nodes.flatMap {
      case block: Block => block.insts.toList
      case _            => Nil
    }

    /** what each local not bound by a call was last bound to, in step order */
    val letDefs: Map[String, Expr] = blockInsts.collect {
      case ILet(lhs, expr)           => localName(lhs) -> expr
      case IAssign(lhs: Local, expr) => localName(lhs) -> expr
    }.toMap

    /** the locals the algorithm returns */
    val returned: Set[String] = blockInsts.collect {
      case IReturn(ERef(local: Local)) => localName(local)
    }.toSet

  // ///////////////////////////////////////////////////////////////////////////
  // Nodes
  // ///////////////////////////////////////////////////////////////////////////

  /** Flattens the graph in node-id order. The CFG builder hands out ids as it
    * walks the algorithm, so this is the order the steps are written in, and it
    * is stable across runs. The schema carries no successor edges: the analysis
    * is path-insensitive by construction and joins every definition of a name.
    */
  private def lowerNodes(ctx: FuncCtx): Vector[NodeJson] =
    val out = ListBuffer[NodeJson]()
    for (node <- ctx.nodes) node match
      case block: Block =>
        for (inst <- block.insts) lowerInst(ctx, block.id, inst, out)
      case call: Call =>
        lowerCallInst(ctx, call, out)
      case branch: Branch =>
        // An abrupt-check branch is guard machinery, already recorded on the
        // call it guards.
        if (!branch.isAbruptNode) out += NodeJson(NodeKinds.Branch)
    out.toVector

  private def lowerInst(
    ctx: FuncCtx,
    nodeId: Int,
    inst: NormalInst,
    out: ListBuffer[NodeJson],
  ): Unit =
    if (YetCollector(inst, ignoreInAssert = true).nonEmpty)
      // The step is not formalized. Incompleteness is per-step, so the analysis
      // falls back only for the signals this step feeds.
      out += NodeJson(NodeKinds.Opaque)
    else if (!isPrologue(ctx, inst) && !isCompletionUnwrap(inst))
      inst match
        case ILet(lhs, expr) =>
          out += NodeJson(
            NodeKinds.Let,
            target = localName(lhs),
            source = Some(lowerExpr(ctx, expr)),
          )
        case IAssign(ref: Var, expr) =>
          out += NodeJson(
            NodeKinds.Let,
            target = varName(ref),
            source = Some(lowerExpr(ctx, expr)),
          )
        case IAssign(Field(base, key), expr) =>
          out += slotWrite(ctx, base, key, Some(lowerExpr(ctx, expr)))
        case IPush(elem, list, _) =>
          // How "Append _x_ to _O_.[[Slot]]" is spelled. The appended value is
          // what the escape analysis reads.
          val value = Some(lowerExpr(ctx, elem))
          list match
            case ERef(Field(base, key)) =>
              out += slotWrite(ctx, base, key, value)
            case other =>
              out += NodeJson(
                NodeKinds.SlotWrite,
                obj = Some(lowerExpr(ctx, other)),
                value = value,
              )
        case IPop(lhs, list, _) =>
          // Reading an element out of a list yields a different object from the
          // list, so the origin chain stops here.
          out += NodeJson(
            NodeKinds.Let,
            target = localName(lhs),
            source = Some(
              ExprJson(ExprKinds.Prop, obj = Some(lowerExpr(ctx, list))),
            ),
          )
        case IExpand(base, key) =>
          out += slotWrite(ctx, base, key, None)
        case IDelete(base, key) =>
          out += slotWrite(ctx, base, key, None)
        case IReturn(expr) =>
          for (value <- returnValue(ctx, nodeId, expr))
            out += NodeJson(NodeKinds.Return, value = Some(value))
        case IExpr(_) | IAssert(_) | IPrint(_) | INop() => ()

  /** Lowers a write through a reference. The slot name comes out without its
    * `[[ ]]` brackets, which is how the IR spells it, so `[[MapData]]` is
    * `MapData` here and in the analysis that reads it back.
    */
  private def slotWrite(
    ctx: FuncCtx,
    base: Ref,
    key: Expr,
    value: Option[ExprJson],
  ): NodeJson =
    val obj = Some(lowerRef(ctx, base))
    key match
      case EStr(slot) =>
        NodeJson(NodeKinds.SlotWrite, obj = obj, slot = slot, value = value)
      case _ =>
        NodeJson(NodeKinds.SlotWrite, obj = obj, value = value)

  /** Decides whether a return carries a value the alias classifier should read.
    *
    * Two returns in the compiled IR are control flow rather than a result. On
    * the then-edge of an abrupt check the algorithm forwards the guarded call's
    * completion, and after a `ThrowCompletion` it returns the thrown
    * completion. Both are already recorded, as the call's guard and as a
    * `throw` node.
    */
  private def returnValue(
    ctx: FuncCtx,
    nodeId: Int,
    expr: Expr,
  ): Option[ExprJson] = expr match
    case ERef(local: Local)
        if ctx.abruptThens.get(nodeId).contains(localName(local)) =>
      None
    case ERef(local: Local) if ctx.throwResults(localName(local)) =>
      None
    case _ => Some(lowerExpr(ctx, expr))

  // ///////////////////////////////////////////////////////////////////////////
  // Calls
  // ///////////////////////////////////////////////////////////////////////////

  private def lowerCallInst(
    ctx: FuncCtx,
    call: Call,
    out: ListBuffer[NodeJson],
  ): Unit =
    val guard = ctx.guards.getOrElse(call.id, Guards.Plain)
    call.callInst match
      case ICall(_, EClo("ThrowCompletion", _), List(thrown)) =>
        out += throwNode(ctx, thrown)
      case ICall(lhs, fexpr, args) =>
        out += NodeJson(
          NodeKinds.Call,
          target = localName(lhs),
          callee = calleeName(fexpr),
          args = args.map(lowerExpr(ctx, _)),
          guard = guard,
        )
      case ISdoCall(lhs, base, op, args) =>
        out += NodeJson(
          NodeKinds.Call,
          target = localName(lhs),
          callee = op,
          args = (base :: args).map(lowerExpr(ctx, _)),
          guard = guard,
        )

  /** Lowers `Throw a *T* exception`, which the compiler spells as a call
    * constructing the error object followed by a `ThrowCompletion` of it. The
    * class name is the intrinsic the error object was built from; a thrown
    * value the algorithm did not construct keeps its expression instead, so the
    * throw-set analysis can trace where it came from.
    */
  private def throwNode(ctx: FuncCtx, thrown: Expr): NodeJson =
    val constructed = thrown match
      case ERef(local: Local) =>
        ctx.callDefs.get(localName(local)).collect {
          case ("__NEW_ERROR_OBJ__", List(EStr(intrinsic))) =>
            errorClass(intrinsic)
        }
      case _ => None
    constructed match
      case Some(cls) => NodeJson(NodeKinds.Throw, errorType = cls)
      case None =>
        NodeJson(NodeKinds.Throw, value = Some(lowerExpr(ctx, thrown)))

  private def calleeName(fexpr: Expr): String = fexpr match
    case EClo(fname, _) => fname
    case ECont(fname)   => fname
    // A dynamic dispatch to an internal method, `O.Set(O, P, V, O)`. The
    // written object stays visible as the first argument.
    case ERef(Field(_, EStr(method))) => method
    // A function held in a variable, such as a callback parameter. The analysis
    // tells this apart from an abstract operation through the origin map.
    case ERef(v: Var) => varName(v)
    case _            => ""

  /** Reconstructs a call's completion guard.
    *
    * The compiler discards the spec's `?` and `!` sugar and lowers each into
    * fixed control flow, so the guard has to be read back off the shape that
    * follows the call. `?` emits an assertion that the result is a completion
    * and then an abrupt check that forwards it; `!` emits an assertion that the
    * result is normal and unwraps it; a plain call asserts nothing.
    */
  private def guardOf(call: Call): String =
    val lhs = call.callInst.lhs
    val assertion = call.next
      .collect { case block: Block => block }
      .flatMap(_.insts.headOption)
      .collect {
        case IAssert(ETypeCheck(ERef(local: Local), ty)) if local == lhs =>
          ty.ty
      }
    if (assertion.contains(CompT)) Guards.Question
    else if (assertion.contains(NormalT)) Guards.Bang
    else Guards.Plain

  // ///////////////////////////////////////////////////////////////////////////
  // The builtin argument prologue
  // ///////////////////////////////////////////////////////////////////////////

  /** Whether an instruction belongs to the argument prologue ESMeta generates
    * in front of every builtin algorithm.
    *
    * A builtin's declared parameters are not IR parameters. The receiver
    * arrives as `this` and the arguments arrive in a list, which the prologue
    * takes each formal out of, defaulting a missing one to `undefined`.
    * Dropping the prologue leaves each formal named only by `params`, where the
    * analysis reads its index from. Keeping it would instead bind those names
    * to the argument list and to `undefined`, neither of which carries a
    * parameter origin.
    */
  private def isPrologue(ctx: FuncCtx, inst: NormalInst): Boolean =
    ctx.isBuiltin && (inst match
      case ILet(ArgsName, _)    => true
      case IExpand(ArgsName, _) => true
      case IPop(lhs, list, _) =>
        isArgsList(list) && ctx.formals(localName(lhs))
      case ILet(Name(name), EUndef()) => ctx.formals(name)
      // A rest parameter takes the whole remaining argument list.
      case ILet(Name(name), list) => ctx.formals(name) && isArgsList(list)
      case _                      => false
    )

  private def isArgsList(expr: Expr): Boolean = expr match
    case ERef(ArgsListName) => true
    case _                  => false

  /** Whether an instruction is the unwrap half of a completion guard, `x =
    * x.Value`. Emitting it would turn the guarded call's result into a slot
    * read, which resolves to an unknown origin and would break every chain that
    * runs through a `?`-guarded coercion such as `Let O be ? ToObject(this)`.
    */
  private def isCompletionUnwrap(inst: NormalInst): Boolean = inst match
    case IAssign(lhs: Local, ERef(Field(base: Local, EStr("Value")))) =>
      lhs == base
    case _ => false

  // ///////////////////////////////////////////////////////////////////////////
  // Expressions
  // ///////////////////////////////////////////////////////////////////////////

  private def lowerExpr(ctx: FuncCtx, expr: Expr): ExprJson = expr match
    case ERef(ref)      => lowerRef(ctx, ref)
    case EClo(fname, _) => ExprJson(ExprKinds.Var, name = fname)
    case ECont(fname)   => ExprJson(ExprKinds.Var, name = fname)
    // Allocations are the fresh origins. Their operands are kept because a
    // parameter stored into a freshly allocated record escapes into whatever
    // that record is then appended to.
    case ERecord(_, pairs) =>
      ExprJson(ExprKinds.Alloc, args = pairs.map((_, v) => lowerExpr(ctx, v)))
    case EList(exprs) =>
      ExprJson(ExprKinds.Alloc, args = exprs.map(lowerExpr(ctx, _)))
    case EMap(_, pairs) =>
      val args =
        pairs.flatMap((k, v) => List(lowerExpr(ctx, k), lowerExpr(ctx, v)))
      ExprJson(ExprKinds.Alloc, args = args)
    case ECopy(obj) =>
      ExprJson(ExprKinds.Alloc, args = List(lowerExpr(ctx, obj)))
    case EKeys(map, _) =>
      ExprJson(ExprKinds.Alloc, args = List(lowerExpr(ctx, map)))
    // Arithmetic, comparisons, coercions to a primitive, and the literals
    // themselves. None of these can carry a receiver or parameter origin.
    case _ => ExprJson(ExprKinds.Lit)

  private def lowerRef(ctx: FuncCtx, ref: Ref): ExprJson = ref match
    case Field(base, EStr(slot)) =>
      ExprJson(ExprKinds.Slot, obj = Some(lowerRef(ctx, base)), slot = slot)
    case Field(base, _) =>
      ExprJson(ExprKinds.Prop, obj = Some(lowerRef(ctx, base)))
    case ThisName if ctx.isBuiltin => ExprJson(ExprKinds.This)
    case v: Var                    => ExprJson(ExprKinds.Var, name = varName(v))

  // ///////////////////////////////////////////////////////////////////////////
  // Promise-returning algorithms
  // ///////////////////////////////////////////////////////////////////////////

  /** Whether the algorithm builds a promise capability and hands back the
    * promise that capability governs. That combination is what makes a raise
    * inside the algorithm a rejection rather than a synchronous throw.
    *
    * A returned value counts when it is the capability's `[[Promise]]`, as in
    * `Promise.reject`, or the result of an operation the capability was handed
    * to, as in `Promise.prototype.then` returning `PerformPromiseThen(…,
    * resultCapability)`. Building a capability alone is not enough:
    * `Promise.withResolvers` builds one and returns a plain object holding its
    * three pieces.
    */
  private def isPromise(ctx: FuncCtx): Boolean =
    val capabilities = capabilityLocals(ctx)
    capabilities.nonEmpty && ctx.returned.exists { name =>
      ctx.letDefs.get(name) match
        case Some(ERef(Field(base: Local, EStr("Promise")))) =>
          capabilities(localName(base))
        case _ =>
          ctx.callDefs
            .get(name)
            .exists((_, args) =>
              args.exists {
                case ERef(local: Local) => capabilities(localName(local))
                case _                  => false
              },
            )
    }

  /** The locals holding a promise capability, following the bindings that pass
    * one along.
    */
  private def capabilityLocals(ctx: FuncCtx): Set[String] =
    var capabilities = ctx.callDefs.collect {
      case (lhs, ("NewPromiseCapability", _)) => lhs
    }.toSet
    var grown = true
    while (grown)
      val next = capabilities ++ ctx.letDefs.collect {
        case (name, ERef(local: Local)) if capabilities(localName(local)) =>
          name
      }
      grown = next.size > capabilities.size
      capabilities = next
    capabilities
