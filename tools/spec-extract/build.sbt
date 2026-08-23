// Wrapper build for the CFG serializer. It names the vendored ESMeta checkout
// as a source dependency, so `sbt run` compiles ESMeta from the pinned
// submodule revision and puts esmeta.cfg on this project's classpath. Nothing
// is published to a local Ivy or Maven repository and the vendored tree is
// never edited.
//
// See README.md for the maintainer runbook.

lazy val esmeta = RootProject(file("esmeta"))

lazy val specExtract = project
  .in(file("."))
  .dependsOn(esmeta)
  .settings(
    name := "spec-extract",
    version := "0.1.0",
    organization := "escalier",
    scalaVersion := "3.3.6",
    scalacOptions := Seq(
      "-deprecation",
      "-feature",
      "-unchecked",
      // Import suggestions recurse until the stack overflows on this classpath.
      // ESMeta's own build turns them off for the same reason.
      // https://github.com/scala/scala3/issues/12876
      "-Ximport-suggestion-timeout",
      "0",
    ),

    // ESMeta resolves every resource path, spec.html included, from
    // ESMETA_HOME. Point it at the vendored checkout so the runbook is a bare
    // `mise run serialize-cfg`.
    Compile / run / fork := true,
    Compile / run / envVars +=
      "ESMETA_HOME" -> (baseDirectory.value / "esmeta").getAbsolutePath,

    // The metalanguage parser recurses deeply over spec.html, so the forked JVM
    // needs a larger stack and heap than the defaults. ESMeta's own launcher
    // sets -Xss4m; extraction plus CFG construction in one process needs more.
    Compile / run / javaOptions ++= Seq("-Xms1g", "-Xmx6g", "-Xss64m"),
    Compile / mainClass := Some("escalier.specextract.Main"),
  )
