package escalier.specextract

import esmeta.compiler.Compiler
import esmeta.ir.Program
import esmeta.spec.Spec

/** Compiles the merged specification, letting ECMA-402's definition of a
  * function beat the IR ESMeta supplies for it by hand.
  *
  * ESMeta writes IR for a handful of algorithms it cannot compile from
  * `spec.html`, and `Compiler` prefers one of those to a compiled algorithm of
  * the same name. Three of them stand in for locale-sensitive functions that
  * ECMA-262 leaves to the host and ECMA-402 defines in full, so on the merged
  * specification the stub hides a definition the graph could carry. Each is a
  * single `yet`, which the analysis reads as knowing nothing about the method.
  *
  * The stubs are left where they are and this compiler declines to prefer them.
  * Editing the vendored checkout would put the toolchain's own state in the way
  * of a run, and `Compiler` is written to be extended.
  *
  * Which stubs those are is derived rather than listed, so a revision that
  * supersedes one more of them is handled. The count is pinned, so ESMeta
  * dropping a stub or ECMA-402 taking on another one fails the run instead of
  * changing what the graph holds unannounced.
  */
final class MergedCompiler(spec: Spec) extends Compiler(spec):

  /** The manual stubs ECMA-402 supersedes, by compiled function name.
    *
    * A stub qualifies when the merged specification defines an algorithm of the
    * same name and that algorithm came from ECMA-402. The ECMA-262 half of the
    * document is left exactly as `Main` compiles it.
    */
  val supersededStubs: Set[String] = (for {
    algo <- spec.algorithms
    if MergedSpec.sourceOf(algo.elem).isDefined
    // `compile` names a function by the normalized head name, and `excluded`
    // is read against that, so the same spelling has to be derived here
    name = normalize(algo.head.fname)
    if manualFuncMap.contains(name)
  } yield name).toSet

  /** The names not to compile.
    *
    * This drops the superseded stubs from what `Compiler` refuses to compile,
    * which is the whole of what this class changes.
    *
    * Overriding it is safe because `addFunc` reads it while `result` compiles,
    * long after construction. Overriding a field the constructor itself reads
    * would leave that field null for the constructor.
    */
  override val excluded: Set[String] =
    (manualFuncMap.keySet -- supersededStubs) ++ shorthands

  /** The compiled program, with each superseded stub dropped.
    *
    * Both the stub and the algorithm it stood in for are compiled by now, and
    * they carry one name between them, so the stub is removed here rather than
    * left for `Lowering.checkUniqueNames` to reject.
    *
    * The stub is picked out by identity. `Func` is a case class whose `algo`
    * field the compiler assigns while it compiles, so a stub's hash changes
    * under any collection that had already placed it.
    */
  lazy val program: Program =
    checkSupersededStubCount()
    val stubs = supersededStubs.toList.flatMap(manualFuncMap.get)
    Program(result.funcs.filterNot(func => stubs.exists(_ eq func)), spec)

  /** Fails the run when the number of superseded stubs leaves the reviewed
    * count.
    *
    * The set is derived from two moving parts, the stubs ESMeta ships and the
    * definitions ECMA-402 writes, and a change to either one is a change to
    * what the graph says about a method. Read the algorithm that took a stub's
    * place before moving this number.
    */
  private def checkSupersededStubCount(): Unit =
    if (supersededStubs.size != MergedCompiler.SupersededStubs)
      throw new IllegalStateException(
        s"ECMA-402 supersedes ${supersededStubs.size} manual stubs, not " +
        s"${MergedCompiler.SupersededStubs}: " +
        s"${supersededStubs.toList.sorted.mkString(", ")}. Read what took " +
        "each one's place before updating the count",
      )

object MergedCompiler:

  /** How many manual stubs ECMA-402 supersedes at the pinned revisions:
    * `Number.prototype.toLocaleString` and the two
    * `String.prototype.toLocale*Case` methods. ESMeta's stub for
    * `TypedArray.prototype.toLocaleString` stays, since ECMA-402 leaves that
    * one alone.
    */
  private val SupersededStubs = 3
