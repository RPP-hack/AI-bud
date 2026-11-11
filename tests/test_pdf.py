import importlib.util
import shutil
import tempfile
import unittest
from pathlib import Path

from rag.ingestion.indexing import index_manifest
from rag.ingestion.loaders import load_pdf
from rag.retrieval import Retriever
from rag.storage import Audience, SQLiteStore


@unittest.skipUnless(importlib.util.find_spec("pypdf"), "install requirements-pdf.txt for PDF tests")
class PdfTests(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.root = Path(temp.name)
        self.pdf = self.root / "test.pdf"

    def test_pdf_page_numbers_and_encryption(self):
        from pypdf import PdfWriter
        from pypdf.generic import DictionaryObject, DecodedStreamObject, NameObject
        writer = PdfWriter()
        writer.add_blank_page(100, 100)
        page = writer.add_blank_page(250, 100)
        font = DictionaryObject({
            NameObject("/Type"): NameObject("/Font"), NameObject("/Subtype"): NameObject("/Type1"),
            NameObject("/BaseFont"): NameObject("/Helvetica")})
        page[NameObject("/Resources")] = DictionaryObject({
            NameObject("/Font"): DictionaryObject({NameObject("/F1"): font})})
        stream = DecodedStreamObject()
        stream.set_data(b"BT /F1 12 Tf 10 50 Td (Jira onboarding) Tj ET")
        page[NameObject("/Contents")] = writer._add_object(stream)
        writer.write(self.pdf)
        manifest = self.root / "manifest.json"
        manifest.write_text('[{"id":"guide","path":"test.pdf","title":"Demo","origin":"demo",'
                            '"source_type":"runbook","team":"demo","roles":["intern"]}]')
        store = SQLiteStore(self.root / "test.sqlite3")
        with self.assertLogs("rag.ingestion.loaders", level="WARNING"):
            index_manifest(manifest, store, ocr=False)
        hit = Retriever(store).retrieve("Jira", audience=Audience("intern", "demo"))[0]
        self.assertEqual((hit.page_number, hit.text, hit.extraction_method), (2, "Jira onboarding", "text"))
        writer.encrypt("sample-password")
        writer.write(self.pdf)
        with self.assertRaisesRegex(ValueError, "Encrypted"):
            load_pdf(self.pdf, ocr=False)

    @unittest.skipUnless(shutil.which("tesseract") and importlib.util.find_spec("pypdfium2")
                         and importlib.util.find_spec("PIL"), "requires PDF rendering and Tesseract")
    def test_image_only_pdf_ocr(self):
        from PIL import Image, ImageDraw, ImageFont
        image = Image.new("RGB", (900, 180), "white")
        draw = ImageDraw.Draw(image)
        draw.text((40, 40), "JIRA PROJECT ACCESS", fill="black", font=ImageFont.load_default(size=56))
        image.save(self.pdf, "PDF", resolution=150)
        page = load_pdf(self.pdf, ocr_language="eng")[0]
        self.assertEqual(page.extraction_method, "ocr")
        self.assertIn("PROJECT ACCESS", page.text.upper())
        with self.assertLogs("rag.ingestion.loaders", level="WARNING"), self.assertRaises(ValueError):
            load_pdf(self.pdf, ocr=False)
