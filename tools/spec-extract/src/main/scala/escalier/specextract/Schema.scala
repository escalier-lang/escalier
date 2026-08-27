package escalier.specextract

import java.io.Writer

/** The `cfg.json` schema.
  *
  * Appendix A of `planning/ecma-262/implementation_plan.md` defines this shape
  * as Go structs; the case classes here mirror them field for field. It is the
  * contract between this serializer and the Go analysis, so a field renamed on
  * one side has to be renamed on the other.
  *
  * Two Scala keywords collide with JSON key names the schema fixes. The field
  * `obj` writes the key `object` and the field `name` on [[ExprJson]] writes
  * the key `var`.
  */
final case class CfgJson(
  specTarget: String,
  funcs: Vector[FuncJson],
)

/** Function kinds. */
object FuncKinds:
  /** `X.prototype.method`; the receiver is the `this` value, not a parameter */
  val BuiltinMethod = "builtin-method"

  /** `X.method` and namespace functions; no receiver */
  val BuiltinStatic = "builtin-static"

  /** everything reachable from the builtin surface that JavaScript cannot call:
    * abstract operations, closures, internal and concrete methods
    */
  val AbstractOp = "abstract-op"

/** One algorithm.
  *
  * @param variadic
  *   the 0-based position of the formal that takes the remaining arguments as a
  *   List, absent when the head declares no such formal. `Function (
  *   ...parameterArgs, bodyArg )` declares one that is not last, so the
  *   position is carried rather than read off the end of `params`.
  */
final case class FuncJson(
  name: String,
  kind: String,
  params: List[String],
  variadic: Option[Int],
  promise: Boolean,
  nodes: Vector[NodeJson],
)

/** Node kinds. */
object NodeKinds:
  val Let = "let"
  val Call = "call"
  val SlotWrite = "slotwrite"
  val Throw = "throw"
  val Return = "return"
  val Branch = "branch"

  /** a step carrying an ESMeta `yet` marker, which the analysis reads as
    * incompleteness for whatever signal that step feeds. `text` carries the
    * prose of the step, which is the evidence for judging what the fallback
    * costs.
    */
  val Opaque = "opaque"

/** Completion-record guards on a call. */
object Guards:
  val Question = "?"
  val Bang = "!"
  val Plain = "plain"

final case class NodeJson(
  kind: String,
  target: String = "",
  source: Option[ExprJson] = None,
  callee: String = "",
  args: List[ExprJson] = Nil,
  guard: String = "",
  obj: Option[ExprJson] = None,
  slot: String = "",
  errorType: String = "",
  value: Option[ExprJson] = None,
  text: List[String] = Nil,
)

/** Expression kinds. */
object ExprKinds:
  val Var = "var"
  val This = "this"
  val Lit = "lit"
  val Call = "call"

  /** a read of `Object.Slot` */
  val Slot = "slot"

  /** a read through a computed key, such as a list index */
  val Prop = "prop"

  /** a record, list, map, or copy allocation. The origin is fresh; `args`
    * carries the operands stored into it, which the escape analysis reads.
    */
  val Alloc = "alloc"

final case class ExprJson(
  kind: String,
  name: String = "",
  callee: String = "",
  args: List[ExprJson] = Nil,
  obj: Option[ExprJson] = None,
  slot: String = "",
)

/** Streams a [[CfgJson]] to a writer.
  *
  * The output is one function per line under a single top-level object, so a
  * spec bump produces a per-function diff instead of one changed line. Fields
  * holding the empty string, the empty list, or `None` are omitted, matching
  * the `omitempty` tags in the Go structs.
  */
object JsonWriter:

  def write(out: Writer, cfg: CfgJson): Unit =
    out.write("{\"specTarget\":")
    writeString(out, cfg.specTarget)
    out.write(",\"funcs\":[\n")
    for ((func, i) <- cfg.funcs.zipWithIndex)
      if (i > 0) out.write(",\n")
      writeFunc(out, func)
    out.write("\n]}\n")

  private def writeFunc(out: Writer, func: FuncJson): Unit =
    out.write("{\"name\":")
    writeString(out, func.name)
    out.write(",\"kind\":")
    writeString(out, func.kind)
    out.write(",\"params\":")
    writeStrings(out, func.params)
    for (position <- func.variadic)
      out.write(",\"variadic\":")
      out.write(position.toString)
    out.write(",\"promise\":")
    out.write(if (func.promise) "true" else "false")
    out.write(",\"nodes\":[")
    for ((node, i) <- func.nodes.zipWithIndex)
      if (i > 0) out.write(",")
      writeNode(out, node)
    out.write("]}")

  private def writeNode(out: Writer, node: NodeJson): Unit =
    out.write("{\"kind\":")
    writeString(out, node.kind)
    writeStrField(out, "target", node.target)
    writeExprField(out, "source", node.source)
    writeStrField(out, "callee", node.callee)
    writeExprsField(out, "args", node.args)
    writeStrField(out, "guard", node.guard)
    writeExprField(out, "object", node.obj)
    writeStrField(out, "slot", node.slot)
    writeStrField(out, "errorType", node.errorType)
    writeExprField(out, "value", node.value)
    writeStringsField(out, "text", node.text)
    out.write("}")

  private def writeExpr(out: Writer, expr: ExprJson): Unit =
    out.write("{\"kind\":")
    writeString(out, expr.kind)
    writeStrField(out, "var", expr.name)
    writeStrField(out, "callee", expr.callee)
    writeExprsField(out, "args", expr.args)
    writeExprField(out, "object", expr.obj)
    writeStrField(out, "slot", expr.slot)
    out.write("}")

  private def writeStrField(out: Writer, key: String, value: String): Unit =
    if (value.nonEmpty)
      out.write(",\"" + key + "\":")
      writeString(out, value)

  private def writeExprField(
    out: Writer,
    key: String,
    value: Option[ExprJson],
  ): Unit = for (expr <- value)
    out.write(",\"" + key + "\":")
    writeExpr(out, expr)

  private def writeExprsField(
    out: Writer,
    key: String,
    exprs: List[ExprJson],
  ): Unit = if (exprs.nonEmpty)
    out.write(",\"" + key + "\":[")
    for ((expr, i) <- exprs.zipWithIndex)
      if (i > 0) out.write(",")
      writeExpr(out, expr)
    out.write("]")

  private def writeStringsField(
    out: Writer,
    key: String,
    values: List[String],
  ): Unit = if (values.nonEmpty)
    out.write(",\"" + key + "\":")
    writeStrings(out, values)

  private def writeStrings(out: Writer, values: List[String]): Unit =
    out.write("[")
    for ((value, i) <- values.zipWithIndex)
      if (i > 0) out.write(",")
      writeString(out, value)
    out.write("]")

  private def writeString(out: Writer, value: String): Unit =
    out.write('"')
    var i = 0
    while (i < value.length)
      val c = value.charAt(i)
      c match
        case '"'          => out.write("\\\"")
        case '\\'         => out.write("\\\\")
        case '\n'         => out.write("\\n")
        case '\r'         => out.write("\\r")
        case '\t'         => out.write("\\t")
        case c if c < ' ' => out.write("\\u%04x".format(c.toInt))
        case c            => out.write(c.toInt)
      i += 1
    out.write('"')
