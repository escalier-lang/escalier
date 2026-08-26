package escalier.specextract

import esmeta.cfgBuilder.CFGBuilder
import esmeta.compiler.Compiler
import esmeta.extractor.Extractor
import java.io.{BufferedWriter, FileOutputStream, OutputStreamWriter}
import java.nio.charset.StandardCharsets.UTF_8

/** Writes `cfg.json`, the serialized control-flow graph of ECMA-262.
  *
  * The pipeline is ESMeta's own `extract → compile → build-cfg` run in process,
  * followed by the lowering in [[Lowering]]. The spec revision comes from the
  * `ecma262` submodule of the vendored ESMeta checkout that `ESMETA_HOME`
  * points at, so there is nothing to pass here. What was extracted is recorded
  * in the output as `specTarget`.
  *
  * The one argument is the output path, defaulting to `cfg.json` beside this
  * build.
  */
object Main:

  def main(args: Array[String]): Unit =
    val out = args.headOption.getOrElse("cfg.json")

    println("extracting the specification from spec.html ...")
    val spec = Extractor()
    println(s"  revision ${spec.version.map(_.toString).getOrElse("unknown")}")
    println(s"  ${spec.algorithms.length} algorithms")

    println("compiling the specification to IR ...")
    val program = new Compiler(spec).result

    println("building the control-flow graph ...")
    val cfg = new CFGBuilder(program).result
    println(s"  ${cfg.funcs.length} functions")

    println("lowering the control-flow graph ...")
    val lowering = new Lowering(cfg)
    val lowered = lowering.result
    val byKind = lowered.funcs.groupBy(_.kind).view.mapValues(_.length).toList
    for ((kind, count) <- byKind.sortBy(_._1)) println(s"  $count $kind")
    for ((phrasing, count) <- lowering.recognizedCounts)
      println(s"  $count steps read as '$phrasing'")

    val writer = new BufferedWriter(
      new OutputStreamWriter(new FileOutputStream(out), UTF_8),
      1 << 16,
    )
    try JsonWriter.write(writer, lowered)
    finally writer.close()
    println(s"wrote $out")

    Validation.validate(out, lowering.builtinCount)
