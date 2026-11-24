import 'dart:typed_data';

import 'package:image_picker/image_picker.dart' as picker;

/// Where a photo comes from when the driver taps Upload.
enum ImageOrigin { camera, gallery }

/// A photo the driver picked, ready to upload.
class PickedImage {
  const PickedImage({required this.bytes, this.contentType = 'image/jpeg', this.name = 'photo.jpg'});

  final Uint8List bytes;

  /// `image/jpeg` or `image/png`.
  final String contentType;
  final String name;
}

/// Picks photos for documents and the vehicle. The app uses
/// [DeviceImageSource]; tests and previews use [FakeImageSource].
abstract class ImageSource {
  /// The photo, or null if the driver cancelled.
  Future<PickedImage?> pick(ImageOrigin origin);
}

/// The image type from a file's first bytes: PNG, JPEG, else [fallback].
String sniffImageType(Uint8List bytes, {String fallback = 'image/jpeg'}) {
  if (bytes.length >= 4 && bytes[0] == 0x89 && bytes[1] == 0x50 && bytes[2] == 0x4E && bytes[3] == 0x47) return 'image/png';
  if (bytes.length >= 3 && bytes[0] == 0xFF && bytes[1] == 0xD8 && bytes[2] == 0xFF) return 'image/jpeg';
  return fallback;
}

/// The phone's camera or gallery (image_picker; a file chooser on the web).
class DeviceImageSource implements ImageSource {
  DeviceImageSource([picker.ImagePicker? imagePicker]) : _picker = imagePicker ?? picker.ImagePicker();

  final picker.ImagePicker _picker;

  @override
  Future<PickedImage?> pick(ImageOrigin origin) async {
    final file = await _picker.pickImage(
      source: origin == ImageOrigin.camera ? picker.ImageSource.camera : picker.ImageSource.gallery,
      // Keeps photos well under the 5 MB limit while documents stay readable.
      maxWidth: 2400,
      maxHeight: 2400,
      imageQuality: 85,
    );
    if (file == null) return null;
    final bytes = await file.readAsBytes();
    return PickedImage(bytes: bytes, contentType: sniffImageType(bytes, fallback: file.mimeType ?? 'image/jpeg'), name: file.name);
  }
}

/// A valid 1×1 PNG.
final samplePhotoBytes = Uint8List.fromList(const [
  137, 80, 78, 71, 13, 10, 26, 10, 0, 0, 0, 13, 73, 72, 68, 82, 0, 0, 0, 1, 0, 0, 0, 1, 8, 6, 0, 0, 0, 31, 21, 196, 137, //
  0, 0, 0, 13, 73, 68, 65, 84, 120, 218, 99, 252, 207, 192, 80, 15, 0, 4, 133, 1, 128, 132, 169, 140, 33, 0, 0, 0, 0, //
  73, 69, 78, 68, 174, 66, 96, 130,
]);

/// Hands out a small valid PNG (or [next]) without any platform code.
class FakeImageSource implements ImageSource {
  FakeImageSource({this.next, this.cancel = false});

  /// What the next pick returns; null: a 1×1 PNG.
  PickedImage? next;

  /// When true, every pick is cancelled (returns null).
  bool cancel;

  /// Every pick asked for, in order.
  final List<ImageOrigin> requests = [];

  @override
  Future<PickedImage?> pick(ImageOrigin origin) async {
    requests.add(origin);
    if (cancel) return null;
    return next ?? PickedImage(bytes: samplePhotoBytes, contentType: 'image/png', name: 'photo.png');
  }
}
