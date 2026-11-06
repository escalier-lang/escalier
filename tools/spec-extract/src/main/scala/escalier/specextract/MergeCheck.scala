package escalier.specextract

import esmeta.util.SystemUtils.dumpFile

/** Builds the merged ECMA-262 and ECMA-402 document and reports what went into
  * it, without extracting from it.
  *
  * [[Main]] serializes ECMA-262 alone. Extracting from the merged document
  * needs the two ECMA-402 abstract-operation heads whose type wording ends the
  * run, which is work of its own, so this entry point exercises and checks the
  * merge until [[Main]] reads it.
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

    val algorithms = merged.document.select("emu-alg:not([example])").size
    println(s"  $algorithms algorithm bodies in the merged document")
    println("  merge OK")

    for (out <- args.drop(1).headOption)
      dumpFile(merged.document.outerHtml, out)
      println(s"wrote $out")
