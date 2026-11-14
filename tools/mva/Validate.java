// The VAT return oracle's validator, shared with the SAF-T oracle: an XSD —
// Skatteetaten's skattemeldingformerverdiavgift v1.0, or SAF-T Financial 1.30
// and 1.40 — over each file named on the command line, with the JDK's own
// javax.xml.validation and nothing else. tools/mva/validate.sh and
// tools/saft/validate.sh run it as
//
//   java Validate.java --xsd <the XSD> <xml>...
//
// Every file must validate; the first error of each is printed, and any
// failure exits 1.

import java.io.File;
import java.util.ArrayList;
import java.util.List;
import javax.xml.XMLConstants;
import javax.xml.transform.stream.StreamSource;
import javax.xml.validation.Schema;
import javax.xml.validation.SchemaFactory;
import javax.xml.validation.Validator;
import org.xml.sax.SAXException;

public class Validate {
    public static void main(String[] args) throws Exception {
        File xsd = null;
        List<File> files = new ArrayList<>();
        for (int i = 0; i < args.length; i++) {
            if (args[i].equals("--xsd")) {
                xsd = new File(args[++i]);
            } else {
                files.add(new File(args[i]));
            }
        }
        if (xsd == null || files.isEmpty()) {
            System.err.println("usage: Validate --xsd <xsd> <xml>...");
            System.exit(2);
        }
        SchemaFactory factory = SchemaFactory.newInstance(XMLConstants.W3C_XML_SCHEMA_NS_URI);
        factory.setProperty(XMLConstants.ACCESS_EXTERNAL_DTD, "");
        factory.setProperty(XMLConstants.ACCESS_EXTERNAL_SCHEMA, "");
        Schema schema = factory.newSchema(xsd);
        int failures = 0;
        for (File file : files) {
            Validator validator = schema.newValidator();
            validator.setProperty(XMLConstants.ACCESS_EXTERNAL_DTD, "");
            validator.setProperty(XMLConstants.ACCESS_EXTERNAL_SCHEMA, "");
            try {
                validator.validate(new StreamSource(file));
                System.out.println("ok      " + file);
            } catch (SAXException e) {
                failures++;
                System.out.println("INVALID " + file + ": " + e.getMessage());
            }
        }
        System.out.printf("%d files, %d invalid%n", files.size(), failures);
        System.exit(failures == 0 ? 0 : 1);
    }
}
