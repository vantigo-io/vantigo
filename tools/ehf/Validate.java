// The EHF oracle's validator: the UBL 2.1 XSD, then the EN 16931 and Peppol BIS
// Billing 3.0 Schematron (as XSLT, through Saxon), over each file named on the
// command line. tools/ehf/validate.sh prepares the artefacts and runs it as
//
//   java -cp <Saxon-HE and xmlresolver jars> Validate.java \
//     --xsd <UBL 2.1 xsd/maindoc> --xslt <CEN xslt> --xslt <Peppol xslt> \
//     --manifest <testdata/invalid/manifest.json> [--known-failures <file>] <xml>...
//
// A file in the manifest's directory is an invalid fixture: the set of fatal
// rule ids it trips must equal its manifest entry's `rules`, exactly. Every
// other file is a golden: no XSD error and no fatal assertion, except the ones
// --known-failures lists for it (each must still fire, so the list stays true).
// Warnings are printed, never failed on. The SVRL is read here, not judged by a
// library: a failed assert or a successful report with flag="fatal" is fatal,
// anything else a warning.

import java.nio.file.Files;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.TreeSet;
import javax.xml.XMLConstants;
import javax.xml.parsers.DocumentBuilderFactory;
import javax.xml.transform.stream.StreamSource;
import javax.xml.validation.Schema;
import javax.xml.validation.SchemaFactory;
import javax.xml.validation.Validator;
import net.sf.saxon.s9api.Processor;
import net.sf.saxon.s9api.QName;
import net.sf.saxon.s9api.XPathCompiler;
import net.sf.saxon.s9api.XPathSelector;
import net.sf.saxon.s9api.XdmAtomicValue;
import net.sf.saxon.s9api.XdmDestination;
import net.sf.saxon.s9api.XdmItem;
import net.sf.saxon.s9api.XdmNode;
import net.sf.saxon.s9api.XsltExecutable;
import net.sf.saxon.s9api.XsltTransformer;
import org.xml.sax.ErrorHandler;
import org.xml.sax.SAXParseException;

public class Validate {
    static final String INVOICE_NS = "urn:oasis:names:specification:ubl:schema:xsd:Invoice-2";
    static final String CREDIT_NOTE_NS = "urn:oasis:names:specification:ubl:schema:xsd:CreditNote-2";
    static final String SVRL_NS = "http://purl.oclc.org/dsdl/svrl";

    record Finding(String id, boolean fatal, String text, String location) {}

    public static void main(String[] args) throws Exception {
        Path xsdDir = null;
        Path manifest = null;
        Path knownFailures = null;
        List<Path> xslts = new ArrayList<>();
        List<Path> files = new ArrayList<>();
        for (int i = 0; i < args.length; i++) {
            switch (args[i]) {
                case "--xsd" -> xsdDir = Path.of(args[++i]);
                case "--xslt" -> xslts.add(Path.of(args[++i]));
                case "--manifest" -> manifest = Path.of(args[++i]);
                case "--known-failures" -> knownFailures = Path.of(args[++i]);
                default -> files.add(Path.of(args[i]));
            }
        }
        if (xsdDir == null || manifest == null || xslts.isEmpty() || files.isEmpty()) {
            System.err.println("usage: Validate --xsd <dir> --xslt <file>... --manifest <json> [--known-failures <file>] <xml>...");
            System.exit(2);
        }

        Processor saxon = new Processor(false);
        List<XsltExecutable> stylesheets = new ArrayList<>();
        for (Path xslt : xslts) {
            stylesheets.add(saxon.newXsltCompiler().compile(new StreamSource(xslt.toFile())));
        }
        SchemaFactory schemas = SchemaFactory.newInstance(XMLConstants.W3C_XML_SCHEMA_NS_URI);
        Map<String, Schema> schemaByRoot = Map.of(
                INVOICE_NS, schemas.newSchema(xsdDir.resolve("UBL-Invoice-2.1.xsd").toFile()),
                CREDIT_NOTE_NS, schemas.newSchema(xsdDir.resolve("UBL-CreditNote-2.1.xsd").toFile()));

        Map<String, Set<String>> expected = readManifest(saxon, manifest);
        Path invalidDir = manifest.toAbsolutePath().getParent();
        Map<String, Set<String>> known = knownFailures == null ? Map.of() : readKnownFailures(knownFailures);

        XPathCompiler xpath = saxon.newXPathCompiler();
        xpath.declareNamespace("svrl", SVRL_NS);
        XPathSelector assertions = xpath.compile("//svrl:failed-assert | //svrl:successful-report").load();

        int failures = 0;
        int goldens = 0;
        int invalids = 0;
        int warnings = 0;
        int knownSeen = 0;
        Set<String> seenInvalid = new TreeSet<>();
        for (Path file : files) {
            String name = file.getFileName().toString();
            boolean invalid = file.toAbsolutePath().getParent().equals(invalidDir);
            List<String> problems = new ArrayList<>();

            List<String> xsdErrors = validateXsd(file, schemaByRoot);
            for (String e : xsdErrors) {
                problems.add("XSD: " + e);
            }

            List<Finding> findings = new ArrayList<>();
            for (XsltExecutable stylesheet : stylesheets) {
                XsltTransformer t = stylesheet.load();
                t.setSource(new StreamSource(file.toFile()));
                XdmDestination svrl = new XdmDestination();
                t.setDestination(svrl);
                t.transform();
                assertions.setContextItem(svrl.getXdmNode());
                for (XdmItem item : assertions) {
                    XdmNode n = (XdmNode) item;
                    String text = "";
                    for (XdmNode child : n.children()) {
                        if (child.getNodeName() != null && child.getNodeName().getLocalName().equals("text")) {
                            text = child.getStringValue().trim().replaceAll("\\s+", " ");
                        }
                    }
                    findings.add(new Finding(
                            attr(n, "id"), "fatal".equals(attr(n, "flag")), text, attr(n, "location")));
                }
            }

            Set<String> fatal = new TreeSet<>();
            for (Finding f : findings) {
                if (f.fatal()) {
                    fatal.add(f.id());
                }
            }

            String verdict;
            if (invalid) {
                invalids++;
                seenInvalid.add(name);
                Set<String> want = expected.get(name);
                if (want == null) {
                    problems.add("not in " + manifest.getFileName() + "; every invalid fixture needs an entry");
                } else if (!want.equals(fatal)) {
                    problems.add("fatal set " + fatal + " but the manifest says " + want);
                }
                verdict = "invalid " + fatal;
            } else {
                goldens++;
                Set<String> tolerated = known.getOrDefault(name, Set.of());
                Set<String> unexpected = new TreeSet<>(fatal);
                unexpected.removeAll(tolerated);
                if (!unexpected.isEmpty()) {
                    problems.add("fatal " + unexpected);
                }
                Set<String> stale = new TreeSet<>(tolerated);
                stale.removeAll(fatal);
                if (!stale.isEmpty()) {
                    problems.add("known failure " + stale + " no longer fires; remove it from known-failures.txt");
                }
                Set<String> knownHere = new TreeSet<>(tolerated);
                knownHere.retainAll(fatal);
                knownSeen += knownHere.size();
                verdict = !problems.isEmpty() ? "not green"
                        : knownHere.isEmpty() ? "green" : "known failure " + knownHere;
            }

            String status = problems.isEmpty() ? "ok  " : "FAIL";
            System.out.println(status + " " + (invalid ? "invalid/" : "golden/") + name + ": " + verdict);
            for (String p : problems) {
                System.out.println("       " + p);
            }
            for (Finding f : findings) {
                // An invalid fixture's fatals are its point; a golden's are already
                // listed above. Either way the text helps whoever reads the log.
                String kind = f.fatal() ? "fatal  " : "warning";
                if (!f.fatal()) {
                    warnings++;
                }
                System.out.println("       " + kind + " " + f.id() + ": " + f.text() + " @ " + f.location());
            }
            if (!problems.isEmpty()) {
                failures++;
            }
        }

        for (String name : expected.keySet()) {
            if (!seenInvalid.contains(name)) {
                System.out.println("FAIL invalid/" + name + ": in the manifest but not validated (missing file?)");
                failures++;
            }
        }

        System.out.printf("EHF oracle: %d goldens, %d invalid fixtures, %d known failures, %d warnings, %d failing%n",
                goldens, invalids, knownSeen, warnings, failures);
        System.exit(failures == 0 ? 0 : 1);
    }

    static String attr(XdmNode n, String name) {
        String v = n.getAttributeValue(new QName(name));
        return v == null ? "" : v;
    }

    static List<String> validateXsd(Path file, Map<String, Schema> schemaByRoot) throws Exception {
        DocumentBuilderFactory dbf = DocumentBuilderFactory.newInstance();
        dbf.setNamespaceAware(true);
        String root = dbf.newDocumentBuilder().parse(file.toFile()).getDocumentElement().getNamespaceURI();
        Schema schema = schemaByRoot.get(root);
        if (schema == null) {
            return List.of("root element namespace " + root + " is neither a UBL Invoice nor a CreditNote");
        }
        List<String> errors = new ArrayList<>();
        Validator v = schema.newValidator();
        v.setErrorHandler(new ErrorHandler() {
            public void warning(SAXParseException e) {}

            public void error(SAXParseException e) {
                errors.add(e.getLineNumber() + ":" + e.getColumnNumber() + " " + e.getMessage());
            }

            public void fatalError(SAXParseException e) {
                errors.add(e.getLineNumber() + ":" + e.getColumnNumber() + " " + e.getMessage());
            }
        });
        v.validate(new StreamSource(file.toFile()));
        return errors;
    }

    // The manifest through XPath 3.1's json-doc, so the JDK needs no JSON library.
    static Map<String, Set<String>> readManifest(Processor saxon, Path manifest) throws Exception {
        XPathCompiler c = saxon.newXPathCompiler();
        c.declareVariable(new QName("m"));
        XPathSelector s = c.compile("for $f in json-doc($m)?fixtures?* return string-join(($f?file, $f?rules?*), ' ')").load();
        s.setVariable(new QName("m"), new XdmAtomicValue(manifest.toAbsolutePath().toUri().toString()));
        Map<String, Set<String>> out = new LinkedHashMap<>();
        for (XdmItem item : s) {
            String[] parts = item.getStringValue().split(" ");
            Set<String> rules = new TreeSet<>(List.of(parts).subList(1, parts.length));
            if (out.put(parts[0], rules) != null) {
                throw new IllegalStateException("manifest lists " + parts[0] + " twice");
            }
        }
        return out;
    }

    // Lines "<golden file> <rule id>"; '#' starts a comment (the reason).
    static Map<String, Set<String>> readKnownFailures(Path file) throws Exception {
        Map<String, Set<String>> out = new LinkedHashMap<>();
        for (String line : Files.readAllLines(file)) {
            String l = line.replaceAll("#.*", "").trim();
            if (l.isEmpty()) {
                continue;
            }
            String[] parts = l.split("\\s+");
            if (parts.length != 2) {
                throw new IllegalStateException(file + ": expected '<golden file> <rule id>', got: " + line);
            }
            out.computeIfAbsent(parts[0], k -> new TreeSet<>()).add(parts[1]);
        }
        return out;
    }
}
