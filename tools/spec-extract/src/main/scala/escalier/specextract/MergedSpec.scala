package escalier.specextract

import esmeta.SPEC_HTML
import esmeta.spec.Spec
import esmeta.util.HtmlUtils.*
import esmeta.util.ManualInfo
import esmeta.util.SystemUtils.readFile
import org.jsoup.Jsoup
import org.jsoup.nodes.{Document, Element}
import scala.collection.mutable.ListBuffer
import scala.jdk.CollectionConverters.*
import scala.util.matching.Regex

/** Builds the document the extractor reads: ECMA-262's `spec.html` with
  * ECMA-402's clauses spliced into it.
  *
  * The two specifications have to be extracted together. ECMA-402 is built
  * against ECMA-262 as an external bibliography, so its own document holds none
  * of the abstract operations it calls. Every `ToString`, `Construct`, and
  * `Get` in it is a cross-reference to a clause in the other file. The mutation
  * and throw fixpoints resolve a call by name against the functions in one
  * graph, so a graph built from ECMA-402 alone would reach no body for any of
  * them.
  *
  * Splicing works because ESMeta extracts from ecmarkup source rather than from
  * built output. ECMA-402's source is that same form, split across the files
  * `spec/index.html` names in `emu-import` elements. Resolving those imports
  * and appending the result produces a document the stock `Extractor` reads.
  *
  * @param specDir
  *   the `spec` directory of an ECMA-402 checkout, holding `index.html` and the
  *   files it imports
  */
final class MergedSpec(specDir: String):

  /** The merged document and what went into it. */
  lazy val result: MergedSpec.Result =
    val (version, document) = ecma262
    val appended = append(document)
    val superseded = removeSuperseded(document, appended)
    checkSupersededCount(superseded)
    checkTableIds(document)
    rewriteHeads(appended)
    MergedSpec.Result(
      document,
      version,
      importedFiles,
      appended.length,
      superseded,
      MergedSpec.HeadRewrites.map(_.clause),
    )

  /** Reads `spec.html` at the pinned revision, with the bugfix patch ESMeta
    * ships for that revision applied.
    *
    * This is what `Extractor.from` does before it extracts, and reading the
    * file without it would build the graph from a different ECMA-262 than the
    * one `Main` serializes. ESMeta patches the file in place, reads it, and
    * reverts it, so the read is forced while the patch is on disk. The patch is
    * keyed by revision, so a bump that needs none finds none.
    */
  private def ecma262: (Spec.Version, Document) =
    Spec.getVersionWith(None) { version =>
      lazy val document = readFile(SPEC_HTML).toHtml
      for (patch <- ManualInfo.bugfixPatchMap.get(version.hash))
        Spec.applyPatch(patch)
        document
        Spec.clean
      document
    }

  /** The ECMA-402 files `index.html` imports, in the order it names them. */
  private lazy val importedFiles: List[String] =
    val index = Jsoup.parse(readFile(s"$specDir/index.html"))
    val hrefs = index.select("emu-import").asScala.toList.map(_.attr("href"))
    if (hrefs.isEmpty)
      fail(s"$specDir/index.html names no emu-import to read ECMA-402 from")
    hrefs

  /** Appends every ECMA-402 element to the ECMA-262 body.
    *
    * The elements it returns are the roots the superseding definitions are read
    * out of, so the two steps walk the same elements.
    *
    * Each top-level element carries the file it came from, so a report can say
    * which clause of ECMA-402 a function belongs to. The attribute rides along
    * into the extracted algorithm's element, and ESMeta ignores an attribute it
    * does not know.
    */
  private def append(document: Document): List[Element] =
    val body = document.body
    val appended = ListBuffer[Element]()
    for (file <- importedFiles)
      val fragment = Jsoup.parseBodyFragment(readFile(s"$specDir/$file"))
      val children = fragment.body.children.asScala.toList
      if (children.isEmpty)
        fail(s"$specDir/$file holds no element to merge")
      for (child <- children)
        child.attr(MergedSpec.SourceAttr, file)
        body.appendChild(child)
        appended += child
    appended.toList

  /** Removes the ECMA-262 clauses ECMA-402 replaces, and returns their ids.
    *
    * ECMA-402 marks each replacement with a sentence naming the clause it
    * supersedes, so the set is read out of the document rather than listed
    * here. A list would go stale silently against a revision that supersedes
    * one more clause than it used to. A clause more than one sentence names
    * counts once, so the result counts clauses and not sentences.
    *
    * Leaving a replaced clause in place gives two functions under one name, and
    * `Lowering.checkUniqueNames` rejects that, so a missed removal is loud
    * downstream as well.
    */
  private def removeSuperseded(
    document: Document,
    appended: List[Element],
  ): List[String] =
    val ids = (for {
      root <- appended
      paragraph <- root.select("p").asScala.toList
      if paragraph.text.contains(MergedSpec.SupersedesPhrase)
      xref <- paragraph.select("emu-xref[href]").asScala.headOption
      id = xref.attr("href").stripPrefix("#")
    } yield id).distinct
    for (id <- ids)
      Option(document.getElementById(id)) match
        case Some(clause) => clause.remove()
        case None =>
          fail(
            s"ECMA-402 supersedes '$id', which ECMA-262 does not define. " +
            "Check that the two pinned revisions are the same edition",
          )
    ids.sorted

  /** Fails the run when the number of superseded clauses leaves the reviewed
    * count.
    *
    * The set is read out of prose, so a rewording drops a clause from it with
    * no other symptom until a duplicate name surfaces much later. Re-read the
    * superseding clauses against the new wording before moving this number.
    */
  private def checkSupersededCount(ids: List[String]): Unit =
    if (ids.length != MergedSpec.SupersededClauses)
      fail(
        s"ECMA-402 supersedes ${ids.length} ECMA-262 clauses, not " +
        s"${MergedSpec.SupersededClauses}: ${ids.mkString(", ")}. " +
        "Re-read the superseding clauses against the current wording before " +
        "updating the count",
      )

  /** Rewrites the ECMA-402 algorithm heads ESMeta's type vocabulary cannot
    * read.
    *
    * ESMeta raises on a head it cannot parse, and the raise ends extraction for
    * the whole document, so one unreadable head costs every algorithm rather
    * than its own. A step is different: the metalanguage parser falls back to a
    * `yet`, so an unreadable step costs only that step. That difference is why
    * these two are rewritten and the unread steps are left alone.
    *
    * Each rewrite restates a declared type in ECMA-262's vocabulary and leaves
    * the algorithm untouched, so what the graph carries is the operation
    * ECMA-402 wrote. [[MergedSpec.HeadRewrites]] records what each one restates
    * and why the restatement says the same thing.
    */
  private def rewriteHeads(appended: List[Element]): Unit =
    for (rewrite <- MergedSpec.HeadRewrites)
      val clause = appended
        .flatMap(root => Option(root.getElementById(rewrite.clause)))
        .headOption
        .getOrElse(
          fail(s"ECMA-402 no longer defines the clause '${rewrite.clause}'"),
        )
      val head = clause.children.asScala
        .find(_.tagName == "h1")
        .getOrElse(fail(s"the clause '${rewrite.clause}' has no head to read"))
      val before = head.html
      val matches = rewrite.pattern.findAllMatchIn(before).length
      if (matches != 1)
        fail(
          s"the head of '${rewrite.clause}' has $matches places to rewrite, " +
          "not 1. Re-read the head against the current wording before " +
          "changing the pattern",
        )
      head.html(rewrite.pattern.replaceAllIn(before, rewrite.replacement))

  /** Rejects a table id the merged document carries twice.
    *
    * `Extractor.extractTables` keys every `emu-table` by its id in one map, so
    * a repeated id means one table replaces the other and the algorithms
    * reading it silently read the wrong rows. A table with no id is keyed by
    * the empty string, which collides the same way, so it counts like any
    * other. Element ids the two specifications share elsewhere are harmless,
    * since nothing keys on them.
    */
  private def checkTableIds(document: Document): Unit =
    val seen = collection.mutable.Set[String]()
    val repeated = ListBuffer[String]()
    for (table <- document.select("emu-table").asScala.toList)
      val id = table.attr("id")
      if (!seen.add(id)) repeated += id
    if (repeated.nonEmpty)
      val names =
        repeated.distinct.sorted.map(id => if (id.isEmpty) "<no id>" else id)
      fail(
        s"the merged document defines the tables ${names.mkString(", ")} " +
        "more than once, so one replaces the other",
      )

  private def fail(message: String): Nothing =
    throw new IllegalStateException(message)

object MergedSpec:

  /** One merge, and the counts a run reports from it.
    *
    * @param document
    *   the merged document, with every element taken from ECMA-402 carrying
    *   [[SourceAttr]]
    * @param version
    *   the ECMA-262 revision the document was read at
    * @param importedFiles
    *   the ECMA-402 files `index.html` imports
    * @param appendedElements
    *   how many top-level elements ECMA-402 contributed
    * @param supersededClauses
    *   the ids of the ECMA-262 clauses removed because ECMA-402 replaces them,
    *   sorted
    * @param rewrittenHeads
    *   the ids of the ECMA-402 clauses whose head was restated
    */
  final case class Result(
    document: Document,
    version: Spec.Version,
    importedFiles: List[String],
    appendedElements: Int,
    supersededClauses: List[String],
    rewrittenHeads: List[String],
  )

  /** The ECMA-402 file an element came from, or `None` for an ECMA-262 one. */
  def sourceOf(elem: Element): Option[String] =
    var current = elem
    while (current != null)
      if (current.hasAttr(SourceAttr)) return Some(current.attr(SourceAttr))
      current = current.parent
    None

  /** One declared type restated in the vocabulary ESMeta's head parser reads.
    *
    * @param clause
    *   the id of the ECMA-402 clause whose head is rewritten
    * @param pattern
    *   what to restate, which has to match the head exactly once
    * @param replacement
    *   the restatement
    * @param reason
    *   why the restatement declares the same values as the wording it replaces
    */
  private final case class HeadRewrite(
    clause: String,
    pattern: Regex,
    replacement: String,
    reason: String,
  )

  /** The heads ESMeta cannot read at the pinned ECMA-402 revision, reviewed one
    * at a time.
    *
    * Both are ECMA-402 declaring a type in wording ECMA-262 never uses, and
    * neither is about the algorithm under the head. A rewrite belongs here only
    * when the restatement declares the same values, since the graph then
    * carries the operation the specification wrote rather than a different one.
    */
  private val HeadRewrites = List(
    HeadRewrite(
      clause = "sec-canonicalizeuvalue",
      pattern =
        raw"a Unicode locale extension sequence key defined in <a [^>]*>[^<]*</a>".r,
      replacement = "a String",
      reason =
        "A Unicode locale extension sequence key is a String. The algorithm " +
        "reads _ukey_ only by comparing it with String literals, and every " +
        "caller passes one.",
    ),
    HeadRewrite(
      clause = "sec-getbooleanorstringnumberformatoption",
      pattern =
        raw"either a normal completion containing either a Boolean, String, or _fallback_, or a throw completion".r,
      replacement =
        "either a normal completion containing an ECMAScript language value, " +
        "or a throw completion",
      reason =
        "The return type names the parameter _fallback_, which the head " +
        "grammar has no way to spell. _fallback_ is declared an ECMAScript " +
        "language value, and a Boolean and a String are both ECMAScript " +
        "language values, so the union is one.",
    ),
  )

  /** Marks an element as ECMA-402's and names the file it came from. */
  val SourceAttr = "esc-spec-source"

  /** The `spec` directory of the pinned ECMA-402 submodule, relative to the
    * directory `sbt` runs this build from.
    */
  val DefaultSpecDir = "ecma402/spec"

  /** What ECMA-402 writes above a definition that replaces an ECMA-262 one. The
    * clause it replaces is the first `emu-xref` after these words.
    */
  private val SupersedesPhrase = "supersedes the definition provided in"

  /** How many ECMA-262 clauses ECMA-402 replaces at the pinned revisions. Nine
    * are locale-sensitive functions and the tenth is
    * `AvailableNamedTimeZoneIdentifiers`.
    */
  private val SupersededClauses = 10
