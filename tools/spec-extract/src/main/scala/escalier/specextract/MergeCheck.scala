package escalier.specextract

import esmeta.cfg.CFG
import esmeta.cfgBuilder.CFGBuilder
import esmeta.extractor.Extractor
import esmeta.spec.Algorithm
import esmeta.util.SystemUtils.dumpFile

/** Reads the merged ECMA-262 and ECMA-402 document end to end and reports what
  * ECMA-402 contributes to the graph.
  *
  * [[Main]] serializes ECMA-262 alone and writes the `cfg.json` the Go analysis
  * embeds. Committing a merged graph is separate work, so this entry point runs
  * the same `extract → compile → build-cfg` pipeline over the merged document
  * and reports on it without writing anything. It is what says the merge and
  * its head rewrites produce a graph the extractor can read.
  *
  * Usage: `runMain escalier.specextract.MergeCheck [ecma402/spec] [out.html]`.
  * Both arguments are optional. The second writes the merged document out,
  * which is how a clause is read as the extractor sees it.
  */
object MergeCheck:

  def main(args: Array[String]): Unit =
    val specDir = args.headOption.getOrElse(MergedSpec.DefaultSpecDir)

    println(s"merging ECMA-402 from $specDir into spec.html ...")
    val merged = new MergedSpec(specDir).result
    println(s"  ${merged.importedFiles.length} files imported")
    println(s"  ${merged.appendedElements} elements appended")
    println(s"  ${merged.supersededClauses.length} ECMA-262 clauses replaced")
    for (id <- merged.supersededClauses) println(s"    $id")
    println(s"  ${merged.rewrittenHeads.length} algorithm heads rewritten")
    for (id <- merged.rewrittenHeads) println(s"    $id")
    println(s"  ECMA-262 at ${merged.version}")

    // written before anything reads the document, so a run that fails to read
    // it leaves the document on disk to look at
    for (out <- args.drop(1).headOption)
      dumpFile(merged.document.outerHtml, out)
      println(s"  wrote $out")

    println("extracting the merged specification ...")
    val spec = new Extractor(merged.document).result
    val from402 = spec.algorithms.filter(algo => sourceOf(algo).isDefined)
    println(s"  ${spec.algorithms.length} algorithms")
    println(s"  ${from402.length} of them from ECMA-402")
    for ((file, algos) <- from402.groupBy(sourceOf).toList.sortBy(_._1))
      println(s"    ${algos.length}\t${file.getOrElse("")}")

    println("compiling to IR and building the control-flow graph ...")
    val compiler = new MergedCompiler(spec)
    val cfg = new CFGBuilder(compiler.program).result
    println(
      s"  ${compiler.supersededStubs.size} manual stubs ECMA-402 supersedes",
    )
    for (name <- compiler.supersededStubs.toList.sorted) println(s"    $name")
    println(s"  ${cfg.funcs.length} functions")
    report402(cfg)
    println("  merge OK")

  /** Reports the functions ECMA-402 contributed, by kind.
    *
    * The builtins outside the `Intl` namespace are listed one at a time, with
    * the size of the body the graph holds for each. That size is what says
    * whether the definition ECMA-402 writes reached the graph, since a stub
    * standing in for one is a single node.
    */
  private def report402(cfg: CFG): Unit =
    val funcs = cfg.funcs.filter(_.irFunc.algo.flatMap(sourceOf).isDefined)
    println(s"  ${funcs.length} of them from ECMA-402")
    val byKind = funcs.groupBy(_.irFunc.kind.toString).toList.sortBy(_._1)
    for ((kind, group) <- byKind)
      println(
        s"    ${group.length}\t${if (kind.isEmpty) "abstract op" else kind}",
      )
    // Almost every builtin ECMA-402 defines hangs off `Intl`, so dropping
    // those leaves the ECMA-262 methods it supersedes. Three anonymous
    // function clauses come through here too, under the `yet:` name ESMeta
    // gives a builtin path it cannot parse.
    val outsideIntl = funcs
      .filter(_.irFunc.kind == esmeta.ir.FuncKind.Builtin)
      .filter(func => !func.irFunc.name.contains("Intl"))
      .sortBy(_.irFunc.name)
    println(s"  ${outsideIntl.length} builtins outside the Intl namespace")
    for (func <- outsideIntl)
      println(s"    ${func.nodes.size} nodes\t${func.irFunc.name}")

  /** The ECMA-402 file an algorithm came from, or `None` for an ECMA-262 one.
    */
  private def sourceOf(algo: Algorithm): Option[String] =
    MergedSpec.sourceOf(algo.elem)
