package public

// NewHandlerWithSpec — конвейер на подменённом контракте: в публичном контракте пока нет операции
// с bearer, а порядок слоёв на ней закрепляет TestPipelineWired (только тесты).
var NewHandlerWithSpec = newHandler
