package escalier.specextract

import io.circe.{Json, JsonObject}
import io.circe.parser.parse
import scala.collection.mutable.{ListBuffer, Map => MMap}
import scala.io.Source

/** Reads `cfg.json` back and checks it against the schema.
  *
  * This is the §3 gate. The serializer writes JSON by hand, so parsing it back
  * with an independent parser is what makes the round trip a real check rather
  * than a restatement of the writer. On top of well-formedness it checks that
  * every kind is one the schema names, that each node and expression carries
  * the fields its kind defines and no others, and that the builtin surface came
  * through whole.
  */
object Validation:

  private val funcKinds =
    Set(FuncKinds.BuiltinMethod, FuncKinds.BuiltinStatic, FuncKinds.AbstractOp)

  private val guards = Set(Guards.Question, Guards.Bang, Guards.Plain)

  /** The fields each node kind may carry beyond `kind`, and which of those are
    * required. A `call` node's `callee` is optional because a call through an
    * expression the lowering cannot name leaves it empty.
    */
  private val nodeFields: Map[String, (Set[String], Set[String])] = Map(
    NodeKinds.Let -> (Set("target", "source"), Set("target", "source")),
    NodeKinds.Call ->
    (Set("target", "callee", "args", "guard"), Set("target", "guard")),
    NodeKinds.SlotWrite ->
    (Set("object", "slot", "value"), Set("object")),
    NodeKinds.Throw -> (Set("errorType", "value"), Set()),
    NodeKinds.Return -> (Set("value"), Set("value")),
    NodeKinds.Branch -> (Set(), Set()),
    NodeKinds.Opaque -> (Set("text"), Set("text")),
  )

  private val exprFields: Map[String, (Set[String], Set[String])] = Map(
    ExprKinds.Var -> (Set("var"), Set("var")),
    ExprKinds.This -> (Set(), Set()),
    ExprKinds.Lit -> (Set(), Set()),
    ExprKinds.Call -> (Set("callee", "args"), Set("callee")),
    ExprKinds.Slot -> (Set("object", "slot"), Set("object", "slot")),
    ExprKinds.Prop -> (Set("object"), Set("object")),
    ExprKinds.Alloc -> (Set("args"), Set()),
  )

  /** The methods the §1 spike read its findings from. Between them they span
    * every shape the analysis has to handle, so their canonical keys are the
    * spot-check that the builtin surface is keyed the way Appendix C spells it.
    */
  private val representative = List(
    "Array.prototype.push",
    "Array.prototype.fill",
    "Array.prototype.sort",
    "Array.prototype.slice",
    "Array.prototype.map",
    "Array.prototype.forEach",
    "Map.prototype.set",
    "Set.prototype.add",
    "Object.freeze",
    "Reflect.set",
    "String.prototype.charAt",
    "String.prototype.replace",
    "String.prototype [ @@iterator ]",
    "Number.prototype.toFixed",
    "Promise.reject",
    "Promise.all",
    "get Map.prototype.size",
  )

  /** Checks the written file. Throws when anything fails, after listing every
    * problem found rather than only the first.
    *
    * @param expectedBuiltins
    *   how many builtin algorithms the control-flow graph held, so a builtin
    *   dropped on the way out is caught rather than silently missing
    */
  def validate(path: String, expectedBuiltins: Int): Unit =
    val source = Source.fromFile(path, "UTF-8")
    val text =
      try source.mkString
      finally source.close()

    val root = parse(text).fold(
      failure => fail(s"$path is not valid JSON: ${failure.message}"),
      identity,
    )
    val problems = ListBuffer[String]()
    val obj = objectOf(root, "the document", problems)
    checkKeys(
      obj,
      "the document",
      Set("specTarget", "funcs"),
      Set("specTarget", "funcs"),
      "the document",
      problems,
    )

    for (target <- obj("specTarget"); if target.asString.isEmpty)
      problems += "specTarget is not a string"

    val funcs = obj("funcs").flatMap(_.asArray).getOrElse {
      fail("funcs is not an array")
    }
    val kindCounts = MMap[String, Int]().withDefaultValue(0)
    val nodeCounts = MMap[String, Int]().withDefaultValue(0)
    val guardCounts = MMap[String, Int]().withDefaultValue(0)
    val abstractOps = collection.mutable.Set[String]()
    var promises = 0
    var callNodes = 0
    var resolvedCallees = 0

    for (func <- funcs)
      val where = func.hcursor.get[String]("name").getOrElse("<unnamed>")
      val funcObj = objectOf(func, where, problems)
      checkKeys(
        funcObj,
        where,
        Set("name", "kind", "params", "promise", "nodes"),
        Set("name", "kind", "params", "promise", "nodes"),
        "func",
        problems,
      )
      val kind = funcObj("kind").flatMap(_.asString).getOrElse("")
      if (!funcKinds(kind)) problems += s"$where: unknown func kind '$kind'"
      kindCounts(kind) += 1
      if (kind == FuncKinds.AbstractOp) abstractOps += where
      funcObj("promise").flatMap(_.asBoolean) match
        case None        => problems += s"$where: promise is not a boolean"
        case Some(true)  => promises += 1
        case Some(false) => ()
      for {
        params <- funcObj("params").flatMap(_.asArray)
        param <- params
        if param.asString.isEmpty
      } problems += s"$where: a parameter name is not a string"
      for {
        nodes <- funcObj("nodes").flatMap(_.asArray)
        node <- nodes
      } {
        nodeCounts(node.hcursor.get[String]("kind").getOrElse("")) += 1
        for (guard <- node.hcursor.get[String]("guard").toOption)
          guardCounts(guard) += 1
        checkNode(node, where, problems)
      }

    // A second pass now that every function name is known, so a callee can be
    // matched against the abstract operations it might name.
    for {
      func <- funcs
      where = func.hcursor.get[String]("name").getOrElse("<unnamed>")
      nodes <- func.hcursor.get[Vector[Json]]("nodes").toOption.toList
      node <- nodes
      if node.hcursor.get[String]("kind").toOption.contains(NodeKinds.Call)
    } {
      callNodes += 1
      val callee = node.hcursor.get[String]("callee").getOrElse("")
      if (abstractOps(callee)) resolvedCallees += 1
    }

    val builtins =
      kindCounts(FuncKinds.BuiltinMethod) + kindCounts(FuncKinds.BuiltinStatic)
    if (builtins != expectedBuiltins)
      problems += s"$builtins builtins written, $expectedBuiltins in the graph"

    val names = funcs.flatMap(_.hcursor.get[String]("name").toOption).toSet
    for (name <- representative if !names(name))
      problems += s"the representative method '$name' is missing"

    problems ++= missingSignals(nodeCounts, guardCounts, promises)

    println(s"validating $path ...")
    for ((kind, count) <- kindCounts.toList.sorted) println(s"  $count $kind")
    for ((kind, count) <- nodeCounts.toList.sorted)
      println(s"  $count $kind nodes")
    for ((guard, count) <- guardCounts.toList.sorted)
      println(s"  $count '$guard' guards")
    println(s"  $promises promise-returning functions")
    println(
      s"  $resolvedCallees of $callNodes call nodes name an abstract operation",
    )

    if (problems.nonEmpty)
      for (problem <- problems.take(20)) println(s"  FAIL $problem")
      fail(s"${problems.length} schema problems in $path")
    println("  schema OK")

  /** Names any signal the lowering reconstructs that came out empty.
    *
    * Each of these is recovered by matching a shape ESMeta's IR compiler
    * produces, so a change to that compiler can leave the output schema-valid
    * while silently emptying one of them: every call falling back to a `plain`
    * guard, say, or no `Throw` step being recognized. A count that drops to
    * zero is that failure. The check is deliberately a presence test and not a
    * floor, because a spec bump moves every one of these counts for legitimate
    * reasons, and a floor would fail the bump rather than the drift.
    */
  private def missingSignals(
    nodeCounts: MMap[String, Int],
    guardCounts: MMap[String, Int],
    promises: Int,
  ): List[String] =
    val expected = List(
      "'?' guards" -> guardCounts(Guards.Question),
      "'!' guards" -> guardCounts(Guards.Bang),
      "throw nodes" -> nodeCounts(NodeKinds.Throw),
      "slot writes" -> nodeCounts(NodeKinds.SlotWrite),
      "return nodes" -> nodeCounts(NodeKinds.Return),
      "promise-returning functions" -> promises,
    )
    for ((what, count) <- expected if count == 0)
      yield s"no $what, so the shape they are recovered from has changed"

  private def checkNode(
    node: Json,
    where: String,
    problems: ListBuffer[String],
  ): Unit =
    val obj = objectOf(node, where, problems)
    val kind = obj("kind").flatMap(_.asString).getOrElse("")
    nodeFields.get(kind) match
      case None => problems += s"$where: unknown node kind '$kind'"
      case Some((allowed, required)) =>
        checkKeys(
          obj,
          where,
          allowed + "kind",
          required + "kind",
          s"'$kind' node",
          problems,
        )
        if (
          kind == NodeKinds.Throw && obj.keys.toSet
            .intersect(Set("errorType", "value"))
            .isEmpty
        )
          problems +=
            s"$where: a throw node names neither an error class " +
            s"nor a thrown value"
        for (guard <- obj("guard").flatMap(_.asString) if !guards(guard))
          problems += s"$where: unknown guard '$guard'"
        for {
          text <- obj("text").flatMap(_.asArray)
          entry <- text
          if !entry.asString.exists(_.nonEmpty)
        } problems += s"$where: a step text is not a non-empty string"
        for (key <- List("source", "object", "value"); expr <- obj(key))
          checkExpr(expr, where, problems)
        for {
          args <- obj("args").flatMap(_.asArray)
          arg <- args
        } checkExpr(arg, where, problems)

  private def checkExpr(
    expr: Json,
    where: String,
    problems: ListBuffer[String],
  ): Unit =
    val obj = objectOf(expr, where, problems)
    val kind = obj("kind").flatMap(_.asString).getOrElse("")
    exprFields.get(kind) match
      case None => problems += s"$where: unknown expr kind '$kind'"
      case Some((allowed, required)) =>
        checkKeys(
          obj,
          where,
          allowed + "kind",
          required + "kind",
          s"'$kind' expr",
          problems,
        )
        for (obj2 <- obj("object")) checkExpr(obj2, where, problems)
        for {
          args <- obj("args").flatMap(_.asArray)
          arg <- args
        } checkExpr(arg, where, problems)

  private def checkKeys(
    obj: JsonObject,
    where: String,
    allowed: Set[String],
    required: Set[String],
    what: String,
    problems: ListBuffer[String],
  ): Unit =
    val keys = obj.keys.toSet
    for (key <- keys -- allowed)
      problems += s"$where: $what carries an unknown field '$key'"
    for (key <- required -- keys)
      problems += s"$where: $what is missing '$key'"

  private def objectOf(
    json: Json,
    where: String,
    problems: ListBuffer[String],
  ): JsonObject = json.asObject.getOrElse {
    problems += s"$where: expected an object"
    JsonObject.empty
  }

  private def fail(message: String): Nothing =
    throw new IllegalStateException(message)
