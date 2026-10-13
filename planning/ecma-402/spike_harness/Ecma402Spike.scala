package escalier.specextract

import esmeta.SPEC_HTML
import esmeta.cfgBuilder.CFGBuilder
import esmeta.compiler.Compiler
import esmeta.extractor.Extractor
import esmeta.spec.{Algorithm, Spec}
import esmeta.util.HtmlUtils.*
import esmeta.util.SystemUtils.*
import org.jsoup.Jsoup
import org.jsoup.nodes.{Document, Element}
import scala.collection.mutable.{ListBuffer, Map => MMap}
import scala.jdk.CollectionConverters.*

/** Feasibility spike: can the ECMA-262 pipeline read ECMA-402 as well?
  *
  * Runs the production pipeline over a document that merges ECMA-402's
  * ecmarkup sources into ECMA-262's `spec.html`, then reports how much of
  * ECMA-402 survives each stage. The two questions it answers are whether the
  * merge produces a well-formed graph at all and what fraction of ECMA-402's
  * steps ESMeta's metalanguage parser can read.
  *
  * Usage: `runMain escalier.specextract.Ecma402Spike <ecma402/spec> <outDir>`
  */
object Ecma402Spike:

  /** The attribute the merge stamps on every element taken from ECMA-402,
    * carrying the source file so the report can break results down by clause.
    */
  private val Marker = "esc-402-source"

  /** The ECMA-262 clauses ECMA-402 replaces. Each is named by an `emu-xref`
    * beside the words "supersedes the definition provided in". Leaving one in
    * gives two functions under one name.
    */
  private val Superseded = List(
    "sec-string.prototype.localecompare",
    "sec-string.prototype.tolocalelowercase",
    "sec-string.prototype.tolocaleuppercase",
    "sec-number.prototype.tolocalestring",
    "sec-bigint.prototype.tolocalestring",
    "sec-date.prototype.tolocalestring",
    "sec-date.prototype.tolocaledatestring",
    "sec-date.prototype.tolocaletimestring",
    "sec-array.prototype.tolocalestring",
    "sec-availablenamedtimezoneidentifiers",
  )

  def main(args: Array[String]): Unit =
    val specDir = args(0)
    val outDir = args(1)
    mkdir(outDir)

    println("merging ECMA-402 into spec.html ...")
    val files = importOrder(specDir)
    println(s"  ${files.length} imported files")
    val document = merge(specDir, files)
    dumpFile(document.outerHtml, s"$outDir/merged.html")

    println("extracting the merged specification ...")
    val extractor = new TolerantExtractor(
      document,
      Some(Spec.Version(version(), Some("es2025 + ECMA-402"))),
    )
    val spec = extractor.result
    println(s"  ${spec.algorithms.length} algorithms")
    println(s"  ${extractor.failures.length} algorithms the extractor could not read")
    dumpFile(
      extractor.failures
        .map((file, id, msg) => s"$file\t$id\t${msg.replace("\n", " ")}")
        .sorted
        .mkString("\n") + "\n",
      s"$outDir/head_failures.tsv",
    )

    val bySource = spec.algorithms.groupBy(source)
    val from402 = bySource
    val algoCount = from402.filter(_._1.isDefined).values.map(_.length).sum
    println(s"  $algoCount of them from ECMA-402")

    reportExtraction(specDir, files, document, from402, outDir)

    println("compiling to IR and building the control-flow graph ...")
    val program = new Compiler(spec).result
    val cfg = new CFGBuilder(program).result
    println(s"  ${cfg.funcs.length} functions")

    val funcSource = MMap[String, String]()
    for {
      func <- cfg.funcs
      algo <- func.irFunc.algo
      file <- source(algo)
    } funcSource(func.irFunc.name) = file
    println(s"  ${funcSource.size} of them from ECMA-402")

    reportGraph(cfg, funcSource, outDir)

    println("lowering the graph with the production serializer ...")
    val lowering = new Lowering(cfg)
    try
      val lowered = lowering.result
      println(s"  ${lowered.funcs.length} functions lowered")
      val writer = new java.io.BufferedWriter(
        new java.io.OutputStreamWriter(
          new java.io.FileOutputStream(s"$outDir/cfg.json"),
          java.nio.charset.StandardCharsets.UTF_8,
        ),
        1 << 16,
      )
      try JsonWriter.write(writer, lowered)
      finally writer.close()
      println(s"  wrote $outDir/cfg.json")
      reportLowered(lowered, cfg, funcSource, outDir)
    catch
      case e: IllegalStateException =>
        println(s"  LOWERING FAILED: ${e.getMessage}")

    dumpRepresentative(cfg, outDir)

  // ///////////////////////////////////////////////////////////////////////////
  // Merge
  // ///////////////////////////////////////////////////////////////////////////

  /** Reads the `emu-import` hrefs of `index.html` in document order. ECMA-402
    * splits its sources one clause group per file and names them all there.
    */
  private def importOrder(specDir: String): List[String] =
    val index = Jsoup.parse(readFile(s"$specDir/index.html"))
    index.select("emu-import").asScala.toList.map(_.attr("href"))

  /** Builds the merged document.
    *
    * ECMA-402 builds against ECMA-262 as an external bibliography, so its own
    * output holds none of the abstract operations it calls. The inter-procedural
    * passes need those bodies, so the two documents are merged before extraction
    * rather than extracted separately.
    */
  private def merge(specDir: String, files: List[String]): Document =
    val document = readFile(SPEC_HTML).toHtml
    for (id <- Superseded)
      Option(document.getElementById(id)) match
        case Some(elem) => elem.remove()
        case None =>
          throw new IllegalStateException(s"no ECMA-262 clause '$id' to replace")
    val body = document.body
    for {
      file <- files
      fragment = Jsoup.parseBodyFragment(readFile(s"$specDir/$file"))
      child <- fragment.body.children.asScala.toList
    } {
      child.attr(Marker, file)
      body.appendChild(child)
    }
    document

  /** The ECMA-402 file an algorithm came from, or `None` for ECMA-262. */
  private def source(algo: Algorithm): Option[String] =
    var elem: Element = algo.elem
    while (elem != null)
      if (elem.hasAttr(Marker)) return Some(elem.attr(Marker))
      elem = elem.parent
    None

  /** The pinned ECMA-262 revision, read from the vendored checkout. */
  private def version(): String =
    executeCmd("git rev-parse HEAD", s"${esmeta.ECMA262_DIR}").trim

  // ///////////////////////////////////////////////////////////////////////////
  // Reports
  // ///////////////////////////////////////////////////////////////////////////

  /** Extraction coverage: how many `emu-alg` blocks in each ECMA-402 file
    * produced an algorithm, and how many of each algorithm's steps ESMeta's
    * metalanguage parser read rather than leaving as a `yet`.
    */
  private def reportExtraction(
    specDir: String,
    files: List[String],
    document: Document,
    from402: Map[Option[String], List[Algorithm]],
    outDir: String,
  ): Unit =
    val rows = ListBuffer[String]()
    rows += "file\temu-alg\talgorithms\tsteps\tyet-steps\tincomplete-algorithms"
    var totalAlgs = 0
    var totalSteps = 0
    var totalYet = 0
    var totalBlocks = 0
    var totalIncomplete = 0
    for (file <- files) {
      val blocks = document
        .select(s"[$Marker=$file] emu-alg:not([example])")
        .size
      val algos = from402.getOrElse(Some(file), Nil)
      val steps = algos.map(_.steps.length).sum
      val yets = algos.map(_.incompleteSteps.length).sum
      val incomplete = algos.count(!_.complete)
      if (blocks > 0 || algos.nonEmpty)
        rows += s"$file\t$blocks\t${algos.length}\t$steps\t$yets\t$incomplete"
      totalBlocks += blocks
      totalAlgs += algos.length
      totalSteps += steps
      totalYet += yets
      totalIncomplete += incomplete
    }
    rows += s"TOTAL\t$totalBlocks\t$totalAlgs\t$totalSteps\t$totalYet\t$totalIncomplete"
    val base = from402.getOrElse(None, Nil)
    rows += ("ECMA-262\t-\t" + base.length + "\t" + base.map(_.steps.length).sum +
      "\t" + base.map(_.incompleteSteps.length).sum + "\t" + base.count(!_.complete))
    val text = rows.mkString("\n")
    println(text.split("\n").map("  " + _).mkString("\n"))
    dumpFile(text + "\n", s"$outDir/extraction.tsv")

    // every unread step, with the algorithm it sits in
    val unread = for {
      case (Some(file), algos) <- from402.toList
      algo <- algos
      step <- algo.incompleteSteps
    } yield s"$file\t${algo.name}\t${step.toString.take(200)}"
    dumpFile(unread.sorted.mkString("\n") + "\n", s"$outDir/yet_steps.tsv")

  /** Graph coverage: what ESMeta compiled each ECMA-402 algorithm into. */
  private def reportGraph(
    cfg: esmeta.cfg.CFG,
    funcSource: MMap[String, String],
    outDir: String,
  ): Unit =
    val kinds = MMap[String, Int]()
    for {
      func <- cfg.funcs
      if funcSource.contains(func.irFunc.name)
    } kinds(func.irFunc.kind.toString) = kinds.getOrElse(func.irFunc.kind.toString, 0) + 1
    for ((kind, count) <- kinds.toList.sorted) println(s"  $count $kind")
    val names = cfg.funcs
      .filter(f => funcSource.contains(f.irFunc.name))
      .map(f => s"${funcSource(f.irFunc.name)}\t${f.irFunc.kind}\t${f.irFunc.name}")
    dumpFile(names.sorted.mkString("\n") + "\n", s"$outDir/functions.tsv")

  /** Serializer output: the opaque-node rate of the ECMA-402 functions, which
    * is what the Go analysis reads as incompleteness.
    */
  private def reportLowered(
    lowered: CfgJson,
    cfg: esmeta.cfg.CFG,
    funcSource: MMap[String, String],
    outDir: String,
  ): Unit =
    // An abstract operation keeps its ESMeta name, so it is attributed by name.
    // A builtin is renamed to its canonical key, so it is attributed by the key
    // instead: ECMA-402 owns the `Intl` namespace and the locale-sensitive
    // functions it supersedes, and nothing else.
    val ops = funcSource.keySet.toSet
    val localeSensitive = Set(
      "String.prototype.localeCompare",
      "String.prototype.toLocaleLowerCase",
      "String.prototype.toLocaleUpperCase",
      "Number.prototype.toLocaleString",
      "BigInt.prototype.toLocaleString",
      "Array.prototype.toLocaleString",
      "Date.prototype.toLocaleString",
      "Date.prototype.toLocaleDateString",
      "Date.prototype.toLocaleTimeString",
    )
    // The three anonymous function clauses whose builtin path ESMeta could not
    // parse. It keys each by the clause title instead, which is what a
    // `yet:` name is.
    val yetPaths = Set(
      "Collator Compare Functions",
      "DateTime Format Functions",
      "Number Format Functions",
    )
    def owns(json: FuncJson): Boolean =
      if (json.kind == FuncKinds.AbstractOp) ops.contains(json.name)
      else
        // an accessor is spelled `get X.y`, and a prototype with no name of its
        // own in the language is the root of the path, as in
        // `IntlSegmentsPrototype.containing`
        val bare = json.name.stripPrefix("get ").stripPrefix("set ")
        bare.startsWith("Intl") || localeSensitive.contains(bare) ||
        yetPaths.contains(bare)

    val rows = ListBuffer[String]()
    rows += "kind\tname\tnodes\topaque"
    val mine = lowered.funcs.filter(owns)
    for (json <- mine.sortBy(j => (j.kind, j.name)))
      val opaque = json.nodes.count(_.kind == NodeKinds.Opaque)
      rows += s"${json.kind}\t${json.name}\t${json.nodes.length}\t$opaque"
    dumpFile(rows.mkString("\n") + "\n", s"$outDir/lowered.tsv")

    val expected = cfg.funcs.count(func =>
      funcSource.contains(func.irFunc.name) &&
      func.irFunc.kind == esmeta.ir.FuncKind.Builtin,
    )
    val found = mine.count(_.kind != FuncKinds.AbstractOp)
    if (found != expected)
      println(s"  WARNING: attributed $found builtins, the graph holds $expected")

    for (kind <- List(FuncKinds.BuiltinMethod, FuncKinds.BuiltinStatic, FuncKinds.AbstractOp))
      val group = mine.filter(_.kind == kind)
      val clean = group.count(_.nodes.forall(_.kind != NodeKinds.Opaque))
      println(s"  $kind: ${group.length} from ECMA-402, $clean with no opaque node")

  /** Writes the graph text of the methods the findings read from. */
  private def dumpRepresentative(cfg: esmeta.cfg.CFG, outDir: String): Unit =
    val targets = List(
      "INTRINSICS.Intl.getCanonicalLocales",
      "INTRINSICS.Intl.Collator.prototype.resolvedOptions",
      "INTRINSICS.Intl.NumberFormat.prototype.formatToParts",
      "INTRINSICS.Intl.DateTimeFormat.prototype.formatRange",
      "INTRINSICS.Intl.ListFormat.prototype.format",
      "INTRINSICS.Intl.Locale.prototype.maximize",
      "INTRINSICS.Intl.Segmenter.prototype.segment",
      "INTRINSICS.String.prototype.localeCompare",
      "INTRINSICS.Number.prototype.toLocaleString",
      "INTRINSICS.Array.prototype.toLocaleString",
      "CanonicalizeLocaleList",
      "ResolveLocale",
      "CreateDateTimeFormat",
    )
    mkdir(s"$outDir/cfg")
    for {
      name <- targets
      func <- cfg.funcs.find(_.irFunc.name == name)
    } dumpFile(func.toString, s"$outDir/cfg/${name.replace("/", "_")}.cfg")
    val missing = targets.filterNot(n => cfg.funcs.exists(_.irFunc.name == n))
    if (missing.nonEmpty) println(s"  no function for: ${missing.mkString(", ")}")

/** An [[Extractor]] that records the algorithms it cannot read instead of
  * aborting the run.
  *
  * ESMeta raises on a head it cannot parse, so one unreadable clause ends
  * extraction for the whole document. A step is different: the metalanguage
  * parser has a `yet` fallback, so an unreadable step costs only that step. The
  * spike needs the count of unreadable clauses, not the first one, so this
  * catches the raise and drops the algorithm the way an unparsed head would
  * have to be handled anyway.
  */
final class TolerantExtractor(
  document: Document,
  version: Option[Spec.Version],
) extends Extractor(document, version, false):

  private val failed = ListBuffer[(String, String, String)]()

  /** The file, the clause id, and the parser message of each drop. */
  def failures: List[(String, String, String)] = failed.toList

  override def extractAlgorithm(elem: Element): List[Algorithm] =
    try super.extractAlgorithm(elem)
    catch
      case e: Throwable =>
        val clause = Option(elem.parent).map(_.attr("id")).getOrElse("")
        var source: Element = elem
        var file = "ECMA-262"
        while (source != null)
          if (source.hasAttr("esc-402-source")) { file = source.attr("esc-402-source"); source = null }
          else source = source.parent
        failed.synchronized { failed += ((file, clause, e.getMessage.take(300))) }
        Nil
